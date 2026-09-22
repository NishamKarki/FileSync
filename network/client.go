package network

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PingDevice sends a ping request to the specified address and returns the response
func PingDevice(address string) (*PingResponse, error) {
	transport := &http.Transport{
		Proxy: nil,
	}
	// Create an HTTP client with a timeout
	client := http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}
	// Send a GET request to the /ping endpoint of the specified address
	response, err := client.Get("http://" + address + "/ping")
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()
	// Check if the response status code is not OK (200)
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device returned status: %s", response.Status)
	}
	// Decode the JSON response into a PingResponse struct
	var pingResponse PingResponse
	err = json.NewDecoder(response.Body).Decode(&pingResponse)

	if err != nil {
		return nil, err
	}

	return &pingResponse, nil
}
