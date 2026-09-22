package network

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// PingResponse represents the response from a ping request
type PingResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// pingHandler handles the /ping endpoint and responds with a PingResponse
func pingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// Create a PingResponse indicating the server is online
	response := PingResponse{
		Success: true,
		Message: "FileSync Client is Online",
	}

	json.NewEncoder(w).Encode(response)
}

// StartServer starts the HTTP server on the specified port
func StartServer(port string) {
	// Create a new HTTP ServeMux to handle incoming requests
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", pingHandler)
	// Print server starting message to the console
	fmt.Println("FileSync Network Server Starting...", port)
	fmt.Println("Listening on port:", port)

	err := http.ListenAndServe(":"+port, mux)
	// Check for error during server startup
	if err != nil {
		fmt.Println("Network server error:", err)
	}
}
