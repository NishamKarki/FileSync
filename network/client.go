// /Rabindra Neupane
package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
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
	// Check for errors while decoding the JSON response
	if err != nil {
		return nil, err
	}
	return &pingResponse, nil
}

// SendFile sends a file to the specified address using a POST request
func SendFile(address, filePath string) error {
	// Open the file to be sent
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	// Create the HTTP request body
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	//Add the file to the request body
	filePart, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return err
	}

	// Copy the file contents into the request body
	_, err = io.Copy(filePart, file)
	if err != nil {
		return err
	}

	// Close the multipart writer to finalize the request body
	err = writer.Close()
	if err != nil {
		return err
	}
	// Create a new HTTP request for sending the file
	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/file", &requestBody)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	// Create an HTTP client with a timeout for sending the file
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// Check if the response status code is not OK (200)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("file transfer failed: %s", response.Status)
	}

	fmt.Println("file successfully sent to:", address)
	return nil
}
