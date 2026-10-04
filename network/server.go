// /Rabindra Neupane
package network

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	pingResponse := PingResponse{
		Success:    true,
		Message:    "FileSync Client is Online",
		DeviceName: deviceName,
	}
	json.NewEncoder(responseWriter).Encode(pingResponse)
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
	file, fileHeader, uploadFileErr := request.FormFile("file")
	if uploadFileErr != nil {
		http.Error(responseWriter, "Failed to read uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	// Print the name of the received file to the console
	fmt.Println("Received file:", fileHeader.Filename)

	// Get the current working directory to save the uploaded file
	projectDirectory, projectDirectoryErr := os.Getwd()
	if projectDirectoryErr != nil {
		http.Error(responseWriter, "Failed to get project directory", http.StatusInternalServerError)
		return
	}

	// Create the path to the sync folder within the project directory
	syncFolder := filepath.Join(projectDirectory, "Synced Files")

	// Make sure the sync folder exists, create if it doesn't
	projectDirectoryErr = os.MkdirAll(syncFolder, 0755)
	if projectDirectoryErr != nil {
		http.Error(responseWriter, "Failed to create sync folder", http.StatusInternalServerError)
		return
	}
	// Create the destination file in the sync folder
	destinationPath := filepath.Join(syncFolder, filepath.Base(fileHeader.Filename))
	destinationFile, destinationFileErr := os.Create(destinationPath)
	if destinationFileErr != nil {
		http.Error(responseWriter, "Failed to create destination file", http.StatusInternalServerError)
		return
	}
	defer destinationFile.Close()

	// Copy the received file to the destination file
	_, copyErr := io.Copy(destinationFile, file)
	if copyErr != nil {
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

	// Print server starting message to the console
	fmt.Println("FileSync Network Server Starting...", port)
	fmt.Println("Listening on port:", port)

	//
	serverErr := http.ListenAndServe(":"+port, mux)
	// Check for error during server startup
	if serverErr != nil {
		fmt.Println("Network server error:", serverErr)
	}
}

func chunkHandler(responseWriter http.ResponseWriter, request *http.Request) {
	fmt.Println("Chunk transfer request received")
	// Check if the request method is POST, otherwise return a "Method not allowed" error
	if request.Method != http.MethodPost {
		http.Error(responseWriter, "Only Post Requests are allowed", http.StatusMethodNotAllowed)
		return
	}

	//
	fileName := request.FormValue("fileName")
	chunkIndexText := request.FormValue("chunkIndex")

	if fileName == "" || chunkIndexText == "" {
		http.Error(responseWriter, "Missing chunk metadata", http.StatusBadRequest)
		return
	}

	chunkIndex, chunkIndexErr := strconv.Atoi(chunkIndexText)
	if chunkIndexErr != nil {
		http.Error(responseWriter, "Invalid chunk index", http.StatusBadRequest)
		return
	}

	// Read the uploaded chunk from the request
	chunkFile, _, uploadChunkErr := request.FormFile("Chunk")
	if uploadChunkErr != nil {
		http.Error(responseWriter, "Failed to read uploaded chunk", http.StatusBadRequest)
		return
	}
	defer chunkFile.Close()

	// Get the current working directory to save the uploaded chunk
	projectDirectory, projectDirectoryErr := os.Getwd()
	if projectDirectoryErr != nil {
		http.Error(responseWriter, "Failed to get project directory", http.StatusInternalServerError)
		return
	}

	// Construct the path to the folder where the chunk will be saved
	chunkFolder := filepath.Join(projectDirectory, "File Chunks", filepath.Base(fileName))
	projectDirectoryErr = os.MkdirAll(chunkFolder, 0755)
	if projectDirectoryErr != nil {
		http.Error(responseWriter, "Failed to create chunk folder", http.StatusInternalServerError)
		return
	}

	// Construct the path for the chunk file within the chunk folder
	chunkPath := filepath.Join(chunkFolder, fmt.Sprintf("chunk_%d.chunk", chunkIndex))
	destinationChunk, destinationChunkErr := os.Create(chunkPath)
	if destinationChunkErr != nil {
		http.Error(responseWriter, "Failed to create chunk file", http.StatusInternalServerError)
		return
	}
	defer destinationChunk.Close()

	// Copy the uploaded chunk to the destination chunk file
	_, copyErr := io.Copy(destinationChunk, chunkFile)
	if copyErr != nil {
		http.Error(responseWriter, "Failed to save uploaded chunk", http.StatusInternalServerError)
		return
	}
	fmt.Printf("Chunk %d of %s successfully received\n", chunkIndex, fileName)

	responseWriter.WriteHeader(http.StatusOK)
}
