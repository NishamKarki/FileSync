// Author: Rabindra Neupane
package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Rabindra Neupane
// PingResponse describes the identity and role returned by the ping endpoint.
type PingResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	DeviceName  string `json:"deviceName"`
	DeviceID    string `json:"deviceId"`
	Coordinator bool   `json:"coordinator"`
}

// Rabindra Neupane
// pingHandler returns the local FileSync identity used during discovery.
func pingHandler(responseWriter http.ResponseWriter, _ *http.Request, registry *DeviceRegistry) {
	// Set the response type before writing device information.
	responseWriter.Header().Set("Content-Type", "application/json")

	// Read the computer name and use a safe fallback when unavailable.
	deviceName, deviceNameError := os.Hostname()
	if deviceNameError != nil {
		deviceName = "Unknown Device"
	}
	// Create the basic online response.
	pingResponse := PingResponse{
		Success:    true,
		Message:    "FileSync Client is Online",
		DeviceName: deviceName,
	}
	// Add persistent identity and coordinator role when the registry is ready.
	if registry != nil {
		registrationStatus := registry.Status()
		pingResponse.DeviceID, pingResponse.Coordinator = registrationStatus.DeviceID, registrationStatus.Coordinator
	}
	json.NewEncoder(responseWriter).Encode(pingResponse)
}

// Rabindra Neupane
// StartServer registers FileSync routes and runs the local HTTP server.
// Reconstruction implementation: Nisham Karki. Network integration: Rabindra Neupane.
func StartServer(serverContext context.Context, serverPort string, completeTransfer func(FileMetadata, string) error, registry *DeviceRegistry) {
	// Create the server router and register the discovery endpoint.
	serverRoutes := http.NewServeMux()
	serverRoutes.HandleFunc("/ping", func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
		pingHandler(responseWriter, httpRequest, registry)
	})
	// Register coordinator and chunk-location routes when a registry is available.
	if registry != nil {
		// Connect chunk reporting and lookup to the active coordinator registry.
		serverRoutes.HandleFunc("/chunks/report", registry.reportChunks)
		serverRoutes.HandleFunc("/chunks/locations", registry.locateChunk)
		serverRoutes.Handle("/devices", registry.Handler())
		serverRoutes.Handle("/devices/", registry.Handler())
	}
	// Read the project folder used for transfer staging and chunk storage.
	projectDirectory, projectDirectoryReadingError := os.Getwd()
	if projectDirectoryReadingError != nil {
		fmt.Println("Cannot locate transfer storage:", projectDirectoryReadingError)
		return
	}
	// Register benchmark whole-file transfer and normal incremental transfer routes.
	serverRoutes.Handle("/file", NewWholeFileSendHandler(projectDirectory, completeTransfer))
	serverRoutes.Handle("/transfer/", NewTransferHandler(projectDirectory, completeTransfer))

	// Log the listening port for manual testing.
	fmt.Println("FileSync Network Server Starting...", serverPort)
	fmt.Println("Listening on port:", serverPort)

	// Create the HTTP server with a bounded header timeout.
	networkServer := &http.Server{Addr: ":" + serverPort, Handler: serverRoutes, ReadHeaderTimeout: 5 * time.Second}
	// Create a signal that prevents shutdown after normal server exit.
	serverStopped := make(chan struct{})
	defer close(serverStopped)
	// Stop the server cleanly when the application context is cancelled.
	go func() {
		// Wait for application shutdown or normal server completion.
		select {
		case <-serverContext.Done():
		case <-serverStopped:
			return
		}
		// Give active requests a short window to finish cleanly.
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		// Stop accepting new requests and close the server.
		networkServer.Shutdown(shutdownContext)
	}()
	// Run the server until shutdown or an unexpected listening error.
	networkServerError := networkServer.ListenAndServe()
	// Report unexpected server errors but ignore normal shutdown.
	if networkServerError != nil && !errors.Is(networkServerError, http.ErrServerClosed) {
		fmt.Println("Network server error:", networkServerError)
	}
}

// Rabindra Neupane
// validateFileMetadata rejects unsafe names, inconsistent sizes, invalid version
// relationships, and malformed hashes before any transfer storage is touched.
func validateFileMetadata(metadata FileMetadata) error {
	// Validate the flat file name and reject path components on every platform.
	if metadata.FileName == "" || metadata.FileName == "." || metadata.FileName == ".." ||
		strings.ContainsAny(metadata.FileName, "/\\:\x00") || metadata.FileID != metadata.FileName {
		return fmt.Errorf("Invalid file identity: expected a plain file name and matching file ID")
	}
	// Validate the base-version relationship for new and existing files.
	if metadata.Version < 1 || (metadata.BaseVersion != nil && (*metadata.BaseVersion < 1 || *metadata.BaseVersion >= metadata.Version)) {
		return fmt.Errorf("Invalid version metadata")
	}
	// Validate file and chunk sizes before calculating expected counts.
	if metadata.FileSize < 0 || metadata.ChunkSize <= 0 {
		return fmt.Errorf("Invalid file or chunk size")
	}
	// Calculate and validate the chunk count for the declared file size.
	expectedChunkCount := metadata.FileSize / metadata.ChunkSize
	if metadata.FileSize%metadata.ChunkSize != 0 {
		expectedChunkCount++
	}
	if int64(len(metadata.ChunkHashes)) != expectedChunkCount {
		return fmt.Errorf("Chunk count does not match file size")
	}
	// Create one strict validator for every SHA-256 field.
	validHash := func(value string) bool {
		// Decode the hexadecimal text before checking SHA-256 length and case.
		decodedHash, hashDecodingError := hex.DecodeString(value)
		// Accept only a complete lowercase SHA-256 value.
		return hashDecodingError == nil && len(decodedHash) == sha256.Size && value == strings.ToLower(value)
	}
	// Validate the complete-file hash.
	if !validHash(metadata.FileHash) {
		return fmt.Errorf("Invalid file SHA-256 hash")
	}
	// Validate the optional shared base hash used by conflict detection.
	if metadata.BaseHash != "" && !validHash(metadata.BaseHash) {
		return fmt.Errorf("Invalid base SHA-256 hash")
	}
	// Validate the persistent sender identity.
	deviceID, deviceIDDecodingError := hex.DecodeString(metadata.DeviceID)
	if deviceIDDecodingError != nil || len(deviceID) != 16 || metadata.DeviceID != strings.ToLower(metadata.DeviceID) {
		return fmt.Errorf("A persistent registered sender deviceId is required; update both peers")
	}
	// Validate all current chunk hashes.
	for _, chunkHash := range metadata.ChunkHashes {
		// Reject the transfer when any current chunk hash is malformed.
		if !validHash(chunkHash) {
			return fmt.Errorf("Invalid chunk SHA-256 hash")
		}
	}
	// Validate base chunk hashes used for incremental comparison.
	for _, baseChunkHash := range metadata.BaseChunkHashes {
		// Reject malformed hashes from the shared base version.
		if !validHash(baseChunkHash) {
			return fmt.Errorf("Invalid base chunk SHA-256 hash")
		}
	}
	// Validate revision identities used to detect repeated file contents.
	for _, baseRevision := range metadata.BaseRevisions {
		// Reject malformed identities from the version history.
		if !validHash(baseRevision) {
			return fmt.Errorf("Invalid base revision identity")
		}
	}
	// Verify that an empty file uses the standard SHA-256 value.
	if metadata.FileSize == 0 {
		// Calculate the known SHA-256 value for empty content.
		emptyHash := sha256.Sum256(nil)
		// Require the declared file hash to match the empty content.
		if metadata.FileHash != hex.EncodeToString(emptyHash[:]) {
			return fmt.Errorf("Invalid empty file hash")
		}
	}
	// Return success after every metadata field passes validation.
	return nil
}
