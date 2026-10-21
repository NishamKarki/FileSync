// /Rabindra Neupane
package network

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PingResponse represents the response from a ping request
type PingResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	DeviceName string `json:"deviceName"`
}

// pingHandler handles the /ping endpoint and responds with a PingResponse
func pingHandler(responseWriter http.ResponseWriter, request *http.Request) {
	responseWriter.Header().Set("Content-Type", "application/json")

	// Get the hostname of the device running the server
	deviceName, deviceNameError := os.Hostname()
	if deviceNameError != nil {
		deviceName = "Unknown Device"
	}
	// Create a PingResponse indicating the server is online
	response := PingResponse{
		Success:    true,
		Message:    "FileSync Client is Online",
		DeviceName: deviceName,
	}
	json.NewEncoder(responseWriter).Encode(response)
}

// fileHandler handels incomming file transfer requests and responds with a status code
func fileHandler(responseWriter http.ResponseWriter, request *http.Request) {
	fmt.Println("File transfer request received")
	// Check if the request method is POST, otherwise return a "Method not allowed" error
	if request.Method != http.MethodPost {
		http.Error(responseWriter, "Only Post Requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	// Read the uploaded file from the request
	uploadedFile, uploadedFileHeader, fileReadError := request.FormFile("file")
	if fileReadError != nil {
		http.Error(responseWriter, "Failed to read uploaded file", http.StatusBadRequest)
		return
	}
	defer uploadedFile.Close()
	// Print the name of the received file to the console
	fmt.Println("Received file:", uploadedFileHeader.Filename)

	// Get the current working directory to save the uploaded file
	projectDirectory, directoryLookupError := os.Getwd()
	if directoryLookupError != nil {
		http.Error(responseWriter, "Failed to get project directory", http.StatusInternalServerError)
		return
	}

	// Create the path to the sync folder within the project directory
	syncFolder := filepath.Join(projectDirectory, "Synced Files")

	// Make sure the sync folder exists, create if it doesn't
	syncFolderCreationError := os.MkdirAll(syncFolder, 0755)
	if syncFolderCreationError != nil {
		http.Error(responseWriter, "Failed to create sync folder", http.StatusInternalServerError)
		return
	}
	// Create the destination file in the sync folder
	destinationPath := filepath.Join(syncFolder, filepath.Base(uploadedFileHeader.Filename))
	receivedFile, destinationFileCreationError := os.Create(destinationPath)
	if destinationFileCreationError != nil {
		http.Error(responseWriter, "Failed to create destination file", http.StatusInternalServerError)
		return
	}
	defer receivedFile.Close()

	// Copy the received file to the destination file
	_, fileSaveError := io.Copy(receivedFile, uploadedFile)
	if fileSaveError != nil {
		http.Error(responseWriter, "Failed to save uploaded file", http.StatusInternalServerError)
		return
	}
	fmt.Println("File successfully saved to:", destinationPath)
	responseWriter.WriteHeader(http.StatusOK)
}

// StartServer starts the HTTP server on the specified port
func StartServer(port string) {
	// Create a new HTTP ServeMux to handle incoming ping requests
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", pingHandler)
	// Create a new HTTP ServeMux to handle incoming file transfer requests
	mux.HandleFunc("/file", fileHandler)
	// Create a new HTTP ServeMux to handle incoming chunk transfer requests
	mux.HandleFunc("/chunk", chunkHandler)
	// Compare metadata without writing chunks or replacing the destination file.
	mux.HandleFunc("/file-metadata", fileMetadataHandler)

	// Print server starting message to the console
	fmt.Println("FileSync Network Server Starting...", port)
	fmt.Println("Listening on port:", port)

	err := http.ListenAndServe(":"+port, mux)
	// Check for error during server startup
	if err != nil {
		fmt.Println("Network server error:", err)
	}
}

// fileMetadataHandler accepts the proposed file metadata and reports reusable local chunks.
func fileMetadataHandler(w http.ResponseWriter, r *http.Request) {
	projectDirectory, err := os.Getwd()
	if err != nil {
		http.Error(w, "Failed to locate chunk storage", http.StatusInternalServerError)
		return
	}
	compareFileMetadata(w, r, projectDirectory)
}

// compareFileMetadata takes an explicit storage root so tests never touch user files.
func compareFileMetadata(w http.ResponseWriter, r *http.Request, projectDirectory string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Only POST requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	// Bound metadata independently of the size of the file being synchronized.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var metadata FileMetadata
	if err := decoder.Decode(&metadata); err != nil {
		http.Error(w, "Invalid file metadata", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "Expected one file metadata", http.StatusBadRequest)
		return
	}
	if err := validateFileMetadata(metadata); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result := FileMetadataComparison{NeededChunks: make([]int, 0)}
	for index, expectedHash := range metadata.ChunkHashes {
		chunkPath := filepath.Join(projectDirectory, "File Chunks", metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index))
		chunk, err := os.Open(chunkPath)
		if os.IsNotExist(err) {
			result.NeededChunks = append(result.NeededChunks, index)
			continue
		}
		if err != nil {
			http.Error(w, "Failed to read stored chunk", http.StatusInternalServerError)
			return
		}
		expectedSize := metadata.ChunkSize
		if index == len(metadata.ChunkHashes)-1 {
			expectedSize = metadata.FileSize - int64(index)*metadata.ChunkSize
		}
		hasher := sha256.New()
		// Read at most the expected size plus one to detect oversized chunks.
		// LimitReader avoids allocating a buffer proportional to an untrusted size.
		reader := io.LimitReader(chunk, expectedSize)
		size, readErr := io.Copy(hasher, reader)
		var extra [1]byte
		extraSize, extraErr := chunk.Read(extra[:])
		closeErr := chunk.Close()
		if readErr != nil || (extraErr != nil && extraErr != io.EOF) || closeErr != nil {
			http.Error(w, "Failed to read stored chunk", http.StatusInternalServerError)
			return
		}
		if size != expectedSize || extraSize != 0 || hex.EncodeToString(hasher.Sum(nil)) != expectedHash {
			result.NeededChunks = append(result.NeededChunks, index)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func validateFileMetadata(m FileMetadata) error {
	// Current chunk storage is flat: IDs must match the relative file name.
	// Reject path components on either Windows or Unix, regardless of host OS.
	if m.FileName == "" || m.FileName == "." || m.FileName == ".." ||
		strings.ContainsAny(m.FileName, "/\\:\x00") || m.FileID != m.FileName {
		return fmt.Errorf("Invalid file identity: expected a plain file name and matching file ID")
	}
	if m.Version < 1 || (m.BaseVersion != nil && (*m.BaseVersion < 1 || *m.BaseVersion >= m.Version)) {
		return fmt.Errorf("Invalid version metadata")
	}
	if m.FileSize < 0 || m.ChunkSize <= 0 {
		return fmt.Errorf("Invalid file or chunk size")
	}
	count := m.FileSize / m.ChunkSize
	if m.FileSize%m.ChunkSize != 0 {
		count++
	}
	if int64(len(m.ChunkHashes)) != count {
		return fmt.Errorf("Chunk count does not match file size")
	}
	validHash := func(value string) bool {
		decoded, err := hex.DecodeString(value)
		return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
	}
	if !validHash(m.FileHash) {
		return fmt.Errorf("Invalid file SHA-256 hash")
	}
	for _, hash := range m.ChunkHashes {
		if !validHash(hash) {
			return fmt.Errorf("Invalid chunk SHA-256 hash")
		}
	}
	if m.FileSize == 0 {
		emptyHash := sha256.Sum256(nil)
		if m.FileHash != hex.EncodeToString(emptyHash[:]) {
			return fmt.Errorf("Invalid empty file hash")
		}
	}
	return nil
}

func chunkHandler(responseWriter http.ResponseWriter, request *http.Request) {
	fmt.Println("Chunk transfer request received")
	// Check if the request method is POST, otherwise return a "Method not allowed" error
	if request.Method != http.MethodPost {
		http.Error(responseWriter, "Only Post Requests are allowed", http.StatusMethodNotAllowed)
		return
	}

	fileName := request.FormValue("fileName")
	chunkIndexText := request.FormValue("chunkIndex")
	totalChunksText := request.FormValue("totalChunks")
	expectedChunkHash := request.FormValue("chunkHash")

	// Validate the presence of all required chunk metadata fields
	if fileName == "" || chunkIndexText == "" || totalChunksText == "" || expectedChunkHash == "" {
		http.Error(responseWriter, "Missing chunk metadata", http.StatusBadRequest)
		return
	}
	// Convert the chunk index text to an integer
	chunkIndex, chunkIndexConversionError := strconv.Atoi(chunkIndexText)
	if chunkIndexConversionError != nil {
		http.Error(responseWriter, "Invalid chunk index", http.StatusBadRequest)
		return
	}
	// Convert the total chunks text to an integer
	totalChunks, totalChunksConversionError := strconv.Atoi(totalChunksText)
	if totalChunksConversionError != nil || totalChunks <= 0 {
		http.Error(responseWriter, "Invalid total chunks count", http.StatusBadRequest)
		return
	}
	// Make sure the chunk index is within the valid range
	if chunkIndex < 0 || chunkIndex >= totalChunks {
		http.Error(responseWriter, "Chunk index out of range", http.StatusBadRequest)
		return
	}

	// Log the start of receiving the chunk
	fmt.Printf("Receiving chunk %d of %d for %s\n", chunkIndex+1, totalChunks, fileName)

	// Read the uploaded chunk from the request
	uploadedChunk, _, chunkReadError := request.FormFile("Chunk")
	if chunkReadError != nil {
		http.Error(responseWriter, "Failed to read uploaded chunk", http.StatusBadRequest)
		return
	}
	defer uploadedChunk.Close()

	chunkData, chunkReadAllError := io.ReadAll(uploadedChunk)
	if chunkReadAllError != nil {
		http.Error(responseWriter, "Failed to read chunk data", http.StatusInternalServerError)
		return
	}

	receivedHash := sha256.Sum256(chunkData)
	receivedHashString := hex.EncodeToString(receivedHash[:])

	if receivedHashString != expectedChunkHash {

		fmt.Printf("Chunk %d of %s failed integrity verification", chunkIndex, fileName)
		http.Error(responseWriter, "Chunk  integrity verification failed", http.StatusBadRequest)
		return
	}

	fmt.Printf("Chunk %d of %s passed SHA-256 verification\n", chunkIndex, fileName)

	// Get the current working directory to save the uploaded chunk
	projectDirectoryPath, directoryLookupError := os.Getwd()
	if directoryLookupError != nil {
		http.Error(responseWriter, "Failed to get project directory", http.StatusInternalServerError)
		return
	}

	// Construct the path to the folder where the chunk will be saved
	chunkFolder := filepath.Join(projectDirectoryPath, "File Chunks", filepath.Base(fileName))
	chunkFolderCreationError := os.MkdirAll(chunkFolder, 0755)
	if chunkFolderCreationError != nil {
		http.Error(responseWriter, "Failed to create chunk folder", http.StatusInternalServerError)
		return
	}

	// Construct the path for the chunk file within the chunk folder
	chunkPath := filepath.Join(chunkFolder, fmt.Sprintf("chunk_%d.chunk", chunkIndex))
	receivedChunkFile, chunkFileCreationError := os.Create(chunkPath)
	if chunkFileCreationError != nil {
		http.Error(responseWriter, "Failed to create chunk file", http.StatusInternalServerError)
		return
	}

	// Copy the uploaded chunk to the destination chunk file
	_, chunkSaveError := receivedChunkFile.Write(chunkData)
	if chunkSaveError != nil {
		receivedChunkFile.Close()
		http.Error(responseWriter, "Failed to save uploaded chunk", http.StatusInternalServerError)
		return
	}
	fmt.Printf("Chunk %d of %s successfully received\n", chunkIndex, fileName)

	if closeError := receivedChunkFile.Close(); closeError != nil {
		http.Error(responseWriter, "Failed to close received chunk file", http.StatusInternalServerError)
		return
	}

	allChunksReceived := true
	for i := 0; i < totalChunks; i++ {
		expectedChunkPath := filepath.Join(chunkFolder, fmt.Sprintf("chunk_%d.chunk", i))

		if _, err := os.Stat(expectedChunkPath); err != nil {
			allChunksReceived = false
			break
		}
	}

	if allChunksReceived {
		fmt.Printf("All %d chunks of %s have been received\n", totalChunks, fileName)
		reconstructionError := ReconstructFile(projectDirectoryPath, fileName, totalChunks)
		if reconstructionError != nil {
			fmt.Printf("File reconstructruction failed: %v\n", reconstructionError)
			http.Error(responseWriter, "File reconstructruction failed", http.StatusInternalServerError)
			return
		}

		fmt.Printf("File %s reconstructed successfully\n", fileName)
	}
	responseWriter.WriteHeader(http.StatusOK)
}
