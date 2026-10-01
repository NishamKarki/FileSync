// /Rabindra Neupane
package network

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// PingResponse represents the response from a ping request
type PingResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	DeviceName string `json:"deviceName"`
}

// pingHandler handles the /ping endpoint and responds with a PingResponse
func pingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get the hostname of the device running the server
	deviceName, err := os.Hostname()
	if err != nil {
		deviceName = "Unknown Device"
	}
	// Create a PingResponse indicating the server is online
	response := PingResponse{
		Success:    true,
		Message:    "FileSync Client is Online",
		DeviceName: deviceName,
	}
	json.NewEncoder(w).Encode(response)
}

// fileHandler handels incomming file transfer requests and responds with a status code
func fileHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("File transfer request received")
	// Check if the request method is POST, otherwise return a "Method not allowed" error
	if r.Method != http.MethodPost {
		http.Error(w, "Only Post Requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	// Read the uploaded file from the request
	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to read uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	// Print the name of the received file to the console
	fmt.Println("Received file:", fileHeader.Filename)

	// Get the current working directory to save the uploaded file
	projectDirectory, err := os.Getwd()
	if err != nil {
		http.Error(w, "Failed to get project directory", http.StatusInternalServerError)
		return
	}

	// Create the path to the sync folder within the project directory
	syncFolder := filepath.Join(projectDirectory, "Synced Files")

	// Make sure the sync folder exists, create if it doesn't
	err = os.MkdirAll(syncFolder, 0755)
	if err != nil {
		http.Error(w, "Failed to create sync folder", http.StatusInternalServerError)
		return
	}
	// Create the destination file in the sync folder
	destinationPath := filepath.Join(syncFolder, filepath.Base(fileHeader.Filename))
	destinationFile, err := os.Create(destinationPath)
	if err != nil {
		http.Error(w, "Failed to create destination file", http.StatusInternalServerError)
		return
	}
	defer destinationFile.Close()

	// Copy the received file to the destination file
	_, err = io.Copy(destinationFile, file)
	if err != nil {
		http.Error(w, "Failed to save uploaded file", http.StatusInternalServerError)
		return
	}
	fmt.Println("File successfully saved to:", destinationPath)
	w.WriteHeader(http.StatusOK)
}

// StartServer starts the HTTP server on the specified port
func StartServer(port string) {
	// Create a new HTTP ServeMux to handle incoming ping requests
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", pingHandler)
	// Create a new HTTP ServeMux to handle incoming file transfer requests
	mux.HandleFunc("/file", fileHandler)
	// Print server starting message to the console
	fmt.Println("FileSync Network Server Starting...", port)
	fmt.Println("Listening on port:", port)

	err := http.ListenAndServe(":"+port, mux)
	// Check for error during server startup
	if err != nil {
		fmt.Println("Network server error:", err)
	}
}
