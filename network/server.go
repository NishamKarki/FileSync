package network

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type PingResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := PingResponse{
		Success: true,
		Message: "FileSync Client is Online",
	}

	json.NewEncoder(w).Encode(response)
}

func StartServer(port string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/ping", pingHandler)

	fmt.Println("FileSync Network Server Starting...", port)
	fmt.Println("Listening on port:", port)

	err := http.ListenAndServe(":"+port, mux)

	if err != nil {
		fmt.Println("Network server error:", err)
	}
}
