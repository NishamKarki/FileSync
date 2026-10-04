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
	response, responseErr := client.Get("http://" + address + "/ping")
	if responseErr != nil {
		return nil, responseErr
	}

	defer response.Body.Close()
	// Check if the response status code is not OK (200)
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device returned status: %s", response.Status)
	}
	// Decode the JSON response into a PingResponse struct
	var pingResponse PingResponse
	responseErr = json.NewDecoder(response.Body).Decode(&pingResponse)
	if responseErr != nil {
		return nil, responseErr
	}
	return &pingResponse, nil
}

// SendFile sends a file to the specified address using a POST request
func SendFile(address, filePath string) error {
	// Open the file to be sent
	openfile, openFileErr := os.Open(filePath)
	if openFileErr != nil {
		return openFileErr
	}
	defer openfile.Close()
	// Create the HTTP request body
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	//Add the file to the request body
	filePart, filePartErr := writer.CreateFormFile("file", filepath.Base(filePath))
	if filePartErr != nil {
		return filePartErr
	}

	// Copy the file contents into the request body
	_, copyErr := io.Copy(filePart, openfile)
	if copyErr != nil {
		return copyErr
	}

	// Close the multipart writer to finalize the request body
	filePartErr = writer.Close()
	if filePartErr != nil {
		return filePartErr
	}
	// Create a new HTTP request for sending the file
	sendFileRequest, sendFileRequestErr := http.NewRequest(http.MethodPost, "http://"+address+"/file", &requestBody)
	if sendFileRequestErr != nil {
		return sendFileRequestErr
	}
	sendFileRequest.Header.Set("Content-Type", writer.FormDataContentType())

	// Create an HTTP client with a timeout for sending the file
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	response, sendFileRequestErr := client.Do(sendFileRequest)
	if sendFileRequestErr != nil {
		return sendFileRequestErr
	}
	defer response.Body.Close()

	// Check if the response status code is not OK (200)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("file transfer failed: %s", response.Status)
	}

	fmt.Println("file successfully sent to:", address)
	return nil
}

func SendChunk(address string, fileName string, chunkIndex int, chunkData []byte) error {
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Send the name of the original file being split into chunks
	sendFileNameErr := writer.WriteField("fileName", fileName)
	if sendFileNameErr != nil {
		return sendFileNameErr
	}

	// Send the index of the current chunk being sent
	sendChunkIndexErr := writer.WriteField("chunkIndex", fmt.Sprintf("%d", chunkIndex))
	if sendChunkIndexErr != nil {
		return sendChunkIndexErr
	}

	// Add the chunk data to the request body
	chunkPart, chunkPartErr := writer.CreateFormFile("Chunk", fmt.Sprintf("Chunk_%d.chunk", chunkIndex))
	if chunkPartErr != nil {
		return chunkPartErr
	}

	_, writeChunkDataErr := chunkPart.Write(chunkData)
	if writeChunkDataErr != nil {
		return writeChunkDataErr
	}
	chunkPartErr = writer.Close()
	if chunkPartErr != nil {
		return chunkPartErr
	}

	// Send the chunk data to the receiving device
	request, chunkReceiveErr := http.NewRequest(http.MethodPost, "http://"+address+"/chunk", &requestBody)
	if chunkReceiveErr != nil {
		return chunkReceiveErr
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	response, chunkReceiveErr := client.Do(request)
	if chunkReceiveErr != nil {
		return chunkReceiveErr
	}
	defer response.Body.Close()

	// Check if the chunk transfer was successful
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("chunk transfer failed: %s", response.Status)
	}

	// Print a message indicating the successful transfer of the chunk
	fmt.Printf("Chunk %d of %s successfully sent to %s \n", chunkIndex, fileName, address)
	return nil
}
