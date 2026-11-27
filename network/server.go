// Author: Rabindra Neupane
package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rabindra Neupane
// PingResponse describes the identity and role returned by the ping endpoint.
type PingResponse struct {
	Success       bool   `json:"success"`
	Message       string `json:"message"`
	DeviceName    string `json:"deviceName"`
	Coordinator   bool   `json:"coordinator"`
	CAFingerprint string `json:"caFingerprint,omitempty"`
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
	// Add only the coordinator role and CA fingerprint; keep private keys and device ID out.
	if registry != nil {
		registrationStatus := registry.Status()
		pingResponse.Coordinator = registrationStatus.Coordinator
		if registrationStatus.Coordinator {
			pingResponse.CAFingerprint = registry.CAFingerprint()
		}
	}
	json.NewEncoder(responseWriter).Encode(pingResponse)
}

// Rabindra Neupane
// StartServer registers FileSync routes and runs the local HTTP server.
// Reconstruction implementation: Nisham Karki. Network integration: Rabindra Neupane.
func StartServer(serverContext context.Context, serverPort string, completeTransfer func(FileMetadata, string) error, registry *DeviceRegistry, transferAccepted func(), getFileMetadata ...func(string) (FileMetadata, error)) {
	// Read the project folder used for transfer staging and chunk storage.
	projectDirectory, projectDirectoryReadingError := os.Getwd()
	if projectDirectoryReadingError != nil {
		fmt.Println("Cannot locate transfer storage:", projectDirectoryReadingError)
		return
	}
	// Bind the requested port before creating the serving loop.
	listener, listenerCreationError := net.Listen("tcp", ":"+serverPort)
	if listenerCreationError != nil {
		fmt.Println("Network server error:", listenerCreationError)
		return
	}
	StartServerAtRoot(serverContext, listener, projectDirectory, completeTransfer, registry, transferAccepted, getFileMetadata...)
}

// StartServerAtRoot serves FileSync over HTTPS with mutual TLS from an explicit root and listener.
func StartServerAtRoot(serverContext context.Context, listener net.Listener, projectDirectory string, completeTransfer func(FileMetadata, string) error, registry *DeviceRegistry, transferAccepted func(), getFileMetadata ...func(string) (FileMetadata, error)) {
	chunkIndex := registry.ChunkFileIndex()
	if chunkIndex == nil {
		chunkIndex = NewChunkFileIndex(filepath.Join(projectDirectory, "File Chunks"))
		if registry != nil {
			registry.SetChunkFileIndex(chunkIndex)
		}
	}

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
	// Serve only accepted verified chunks for mTLS-authenticated peer reconstruction requests.
	serverRoutes.HandleFunc("/chunks/data", chunkDataHandler(projectDirectory, registry, chunkIndex))
	// Serve current file metadata for mTLS-authenticated peer pull requests.
	var metadataProvider func(string) (FileMetadata, error)
	if len(getFileMetadata) > 0 {
		metadataProvider = getFileMetadata[0]
	}
	serverRoutes.HandleFunc("/files/metadata", fileMetadataHandler(metadataProvider, registry))
	// Register mTLS-authenticated benchmark whole-file transfer and normal incremental transfer routes.
	serverRoutes.Handle("/file", NewWholeFileSendHandler(projectDirectory, completeTransfer, transferAccepted, registry, chunkIndex))
	serverRoutes.Handle("/transfer/", NewTransferHandlerWithContext(serverContext, projectDirectory, completeTransfer, transferAccepted, registry, chunkIndex))

	// Log the listening port for manual testing.
	fmt.Println("FileSync HTTPS/mTLS Server Starting...", listener.Addr().String())
	fmt.Println("Listening on port:", listener.Addr().String())

	// Create the HTTPS server with mutual TLS and explicit resource timeouts.
	networkServer := &http.Server{
		Handler:           serverRoutes,
		TLSConfig:         registry.ServerTLSConfig(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       90 * time.Second,
	}
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
	// Serve TLS using certificates supplied by ServerTLSConfig (no unencrypted key path needed).
	networkServerError := networkServer.ServeTLS(listener, "", "")
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
	// Require a positive file version counter.
	if metadata.Version < 1 {
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

func fileMetadataHandler(provider func(string) (FileMetadata, error), registry *DeviceRegistry) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			responseWriter.Header().Set("Allow", http.MethodGet)
			http.Error(responseWriter, "Only GET requests are allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, authErr := authenticatePeerMTLS(registry, request, ""); authErr != nil {
			http.Error(responseWriter, authErr.Error(), http.StatusUnauthorized)
			return
		}
		if provider == nil {
			http.Error(responseWriter, "File metadata pull unavailable", http.StatusServiceUnavailable)
			return
		}
		fileName := request.URL.Query().Get("name")
		if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, "/\\:\x00") {
			http.Error(responseWriter, "Invalid file name", http.StatusBadRequest)
			return
		}
		metadata, err := provider(fileName)
		if err != nil {
			http.Error(responseWriter, err.Error(), http.StatusNotFound)
			return
		}
		writeTransferJSON(responseWriter, metadata)
	}
}
