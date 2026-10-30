package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PingResponse represents the response from a ping request
type PingResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	DeviceName  string `json:"deviceName"`
	DeviceID    string `json:"deviceId"`
	Coordinator bool   `json:"coordinator"`
}

// pingHandler handles the /ping endpoint and responds with a PingResponse
// Rabindra Neupane
func pingHandler(responseWriter http.ResponseWriter, httpRequest *http.Request, registry *DeviceRegistry) {
	responseWriter.Header().Set("Content-Type", "application/json")

	// Get the hostname of the device running the server
	deviceName, deviceNameError := os.Hostname()
	if deviceNameError != nil {
		deviceName = "Unknown Device"
	}
	// Create a PingResponse indicating the server is online
	pingResponse := PingResponse{
		Success:    true,
		Message:    "FileSync Client is Online",
		DeviceName: deviceName,
	}
	if registry != nil {
		registrationStatus := registry.Status()
		pingResponse.DeviceID, pingResponse.Coordinator = registrationStatus.DeviceID, registrationStatus.Coordinator
	}
	json.NewEncoder(responseWriter).Encode(pingResponse)
}

// StartServer uses the application's reconstruction method after chunk receipt.
// The callback keeps reconstruction owned by the app without an import cycle.
// Rabindra Neupane
// Reconstruction implementation: Nisham Karki
// Network integration: Rabindra Neupane
func StartServer(serverContext context.Context, serverPort string, completeTransfer func(FileMetadata, string) error, registry *DeviceRegistry) {
	// Create a new HTTP ServeMux to handle incoming ping requests
	serverRoutes := http.NewServeMux()
	serverRoutes.HandleFunc("/ping", func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
		pingHandler(responseWriter, httpRequest, registry)
	})
	if registry != nil {
		serverRoutes.HandleFunc("/chunks/report", registry.reportChunks)
		serverRoutes.HandleFunc("/chunks/locations", registry.locateChunk)
		serverRoutes.Handle("/devices", registry.Handler())
		serverRoutes.Handle("/devices/", registry.Handler())
	}
	// Create a new HTTP ServeMux to handle incoming file transfer requests
	// Whole-file route is attached after locating project storage below.
	// Verified chunk uploads use the isolated /transfer/ workflow.
	// Compare metadata without writing chunks or replacing the destination file.
	serverRoutes.HandleFunc("/file-metadata", fileMetadataHandler)
	projectDirectory, projectDirectoryReadingError := os.Getwd()
	if projectDirectoryReadingError != nil {
		fmt.Println("Cannot locate transfer storage:", projectDirectoryReadingError)
		return
	}
	// Whole-file transfer is kept only for the proposal's benchmark comparison.
	serverRoutes.Handle("/file", NewWholeFileSendHandler(projectDirectory, completeTransfer))
	serverRoutes.Handle("/transfer/", NewTransferHandler(projectDirectory, completeTransfer))

	// Print server starting message to the console
	fmt.Println("FileSync Network Server Starting...", serverPort)
	fmt.Println("Listening on port:", serverPort)

	networkServer := &http.Server{Addr: ":" + serverPort, Handler: serverRoutes, ReadHeaderTimeout: 5 * time.Second}
	serverStopped := make(chan struct{})
	defer close(serverStopped)
	go func() {
		select {
		case <-serverContext.Done():
		case <-serverStopped:
			return
		}
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		networkServer.Shutdown(shutdownContext)
	}()
	networkServerError := networkServer.ListenAndServe()
	// Check for error during server startup
	if networkServerError != nil && !errors.Is(networkServerError, http.ErrServerClosed) {
		fmt.Println("Network server error:", networkServerError)
	}
}

// fileMetadataHandler accepts the proposed file metadata and reports reusable local chunks.
// Rabindra Neupane
func fileMetadataHandler(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	projectDirectory, projectDirectoryReadingError := os.Getwd()
	if projectDirectoryReadingError != nil {
		http.Error(responseWriter, "Failed to locate chunk storage", http.StatusInternalServerError)
		return
	}
	compareFileMetadata(responseWriter, httpRequest, projectDirectory)
}

// compareFileMetadata takes an explicit storage root so tests never touch user files.
// Rabindra Neupane
func compareFileMetadata(responseWriter http.ResponseWriter, httpRequest *http.Request, projectDirectory string) {
	if httpRequest.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", http.MethodPost)
		http.Error(responseWriter, "Only POST requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	// Bound metadata independently of the size of the file being synchronized.
	httpRequest.Body = http.MaxBytesReader(responseWriter, httpRequest.Body, 8<<20)
	metadataDecoder := json.NewDecoder(httpRequest.Body)
	metadataDecoder.DisallowUnknownFields()
	var metadata FileMetadata
	if metadataDecodingError := metadataDecoder.Decode(&metadata); metadataDecodingError != nil {
		http.Error(responseWriter, "Invalid file metadata", http.StatusBadRequest)
		return
	}
	if extraMetadataDataError := metadataDecoder.Decode(&struct{}{}); extraMetadataDataError != io.EOF {
		http.Error(responseWriter, "Expected one file metadata", http.StatusBadRequest)
		return
	}
	if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
		http.Error(responseWriter, metadataValidationError.Error(), http.StatusBadRequest)
		return
	}
	comparisonResult := FileMetadataComparison{NeededChunks: make([]int, 0)}
	for chunkIndex, expectedHash := range metadata.ChunkHashes {
		chunkPath := filepath.Join(projectDirectory, "File Chunks", metadata.FileName, fmt.Sprintf("chunk_%d.chunk", chunkIndex))
		chunkFile, chunkFileOpeningError := os.Open(chunkPath)
		if os.IsNotExist(chunkFileOpeningError) {
			comparisonResult.NeededChunks = append(comparisonResult.NeededChunks, chunkIndex)
			continue
		}
		if chunkFileOpeningError != nil {
			http.Error(responseWriter, "Failed to read stored chunk", http.StatusInternalServerError)
			return
		}
		expectedSize := metadata.ChunkSize
		if chunkIndex == len(metadata.ChunkHashes)-1 {
			expectedSize = metadata.FileSize - int64(chunkIndex)*metadata.ChunkSize
		}
		hasher := sha256.New()
		// Read at most the expected size plus one to detect oversized chunks.
		// LimitReader avoids allocating a buffer proportional to an untrusted size.
		limitedReader := io.LimitReader(chunkFile, expectedSize)
		actualSize, chunkReadingError := io.Copy(hasher, limitedReader)
		var extraByte [1]byte
		extraSize, extraChunkReadingError := chunkFile.Read(extraByte[:])
		chunkFileClosingError := chunkFile.Close()
		if chunkReadingError != nil || (extraChunkReadingError != nil && extraChunkReadingError != io.EOF) || chunkFileClosingError != nil {
			http.Error(responseWriter, "Failed to read stored chunk", http.StatusInternalServerError)
			return
		}
		if actualSize != expectedSize || extraSize != 0 || hex.EncodeToString(hasher.Sum(nil)) != expectedHash {
			comparisonResult.NeededChunks = append(comparisonResult.NeededChunks, chunkIndex)
		}
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	json.NewEncoder(responseWriter).Encode(comparisonResult)
}

// Rabindra Neupane
func validateFileMetadata(metadata FileMetadata) error {
	// Current chunk storage is flat: IDs must match the relative file name.
	// Reject path components on either Windows or Unix, regardless of host OS.
	if metadata.FileName == "" || metadata.FileName == "." || metadata.FileName == ".." ||
		strings.ContainsAny(metadata.FileName, "/\\:\x00") || metadata.FileID != metadata.FileName {
		return fmt.Errorf("Invalid file identity: expected a plain file name and matching file ID")
	}
	if metadata.Version < 1 || (metadata.BaseVersion != nil && (*metadata.BaseVersion < 1 || *metadata.BaseVersion >= metadata.Version)) {
		return fmt.Errorf("Invalid version metadata")
	}
	if metadata.FileSize < 0 || metadata.ChunkSize <= 0 {
		return fmt.Errorf("Invalid file or chunk size")
	}
	expectedChunkCount := metadata.FileSize / metadata.ChunkSize
	if metadata.FileSize%metadata.ChunkSize != 0 {
		expectedChunkCount++
	}
	if int64(len(metadata.ChunkHashes)) != expectedChunkCount {
		return fmt.Errorf("Chunk count does not match file size")
	}
	validHash := func(value string) bool {
		decodedHash, hashDecodingError := hex.DecodeString(value)
		return hashDecodingError == nil && len(decodedHash) == sha256.Size && value == strings.ToLower(value)
	}
	if !validHash(metadata.FileHash) {
		return fmt.Errorf("Invalid file SHA-256 hash")
	}
	if metadata.BaseHash != "" && !validHash(metadata.BaseHash) {
		return fmt.Errorf("Invalid base SHA-256 hash")
	}
	deviceID, deviceIDDecodingError := hex.DecodeString(metadata.DeviceID)
	if deviceIDDecodingError != nil || len(deviceID) != 16 || metadata.DeviceID != strings.ToLower(metadata.DeviceID) {
		return fmt.Errorf("A persistent registered sender deviceId is required; update both peers")
	}
	for _, chunkHash := range metadata.ChunkHashes {
		if !validHash(chunkHash) {
			return fmt.Errorf("Invalid chunk SHA-256 hash")
		}
	}
	for _, baseChunkHash := range metadata.BaseChunkHashes {
		if !validHash(baseChunkHash) {
			return fmt.Errorf("Invalid base chunk SHA-256 hash")
		}
	}
	for _, baseRevision := range metadata.BaseRevisions {
		if !validHash(baseRevision) {
			return fmt.Errorf("Invalid base revision identity")
		}
	}
	if metadata.FileSize == 0 {
		emptyHash := sha256.Sum256(nil)
		if metadata.FileHash != hex.EncodeToString(emptyHash[:]) {
			return fmt.Errorf("Invalid empty file hash")
		}
	}
	return nil
}
