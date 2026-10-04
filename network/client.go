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
// It checks whether another FileSync device is reachable on the local network
// The function returns the PingResponse when the device responds
// successfully or an error if the device cannot be reached
func PingDevice(address string) (*PingResponse, error) {
	transport := &http.Transport{
		Proxy: nil,
	}

	// Limit the ping request to five seconds so that an offline peer
	// does not leave device discovery waiting forever
	syncClient := http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}
	// Send a GET request to the /ping endpoint of the specified address
	response, pingRequestError := syncClient.Get("http://" + address + "/ping")
	if pingRequestError != nil {
		return nil, pingRequestError
	}

	defer response.Body.Close()

	// Consider any non-200 response as a failed FileSync transfer.
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device returned status: %s", response.Status)
	}
	// Decode the JSON response into a PingResponse struct
	var pingResponse PingResponse
	responseDecodeError := json.NewDecoder(response.Body).Decode(&pingResponse)
	if responseDecodeError != nil {
		return nil, responseDecodeError
	}
	return &pingResponse, nil
}

// SendFile sends a complete file from a current FileSync devicec to another
// FileSync device on the local network
// It returns nil when the receiving decice confirms successful transfer with HTTP response
func SendFile(address, filePath string) error {
	// Open the file to be sent
	fileToSend, fileOpenError := os.Open(filePath)
	if fileOpenError != nil {
		return fileOpenError
	}
	defer fileToSend.Close()
	// Build a body containing the file data
	// that will be sent to the receiving FileSync peer.
	var requestBody bytes.Buffer
	fileWriter := multipart.NewWriter(&requestBody)

	//Add the file to the request body
	filePart, filePartCreationError := fileWriter.CreateFormFile("file", filepath.Base(filePath))
	if filePartCreationError != nil {
		return filePartCreationError
	}

	// Copy the file contents into the request body
	_, copyError := io.Copy(filePart, fileToSend)
	if copyError != nil {
		return copyError
	}

	// Close the multipart writer to finalize the request body
	if writerCloseError := fileWriter.Close(); writerCloseError != nil {
		return writerCloseError
	}

	// Create a new HTTP request for sending the file
	request, requestCreationError := http.NewRequest(http.MethodPost, "http://"+address+"/file", &requestBody)
	if requestCreationError != nil {
		return requestCreationError
	}
	request.Header.Set("Content-Type", fileWriter.FormDataContentType())

	// Create an HTTP httpClient with a timeout for sending the file
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	response, requestSendError := httpClient.Do(request)
	if requestSendError != nil {
		return requestSendError
	}
	defer response.Body.Close()

	// Check if the response status code is not OK (200)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("file transfer failed: %s", response.Status)
	}

	fmt.Println("file successfully sent to:", address)
	return nil
}

// SendChunk transfers one individual file chunk to another FileSync
// device on the local network
func SendChunk(address string, fileName string, chunkIndex int, chunkData []byte) error {
	var requestBody bytes.Buffer
	chunkWriter := multipart.NewWriter(&requestBody)

	// Send the name of the original file being split into chunks
	sendFileNameErr := chunkWriter.WriteField("fileName", fileName)
	if sendFileNameErr != nil {
		return sendFileNameErr
	}

	// Send the index of the current chunk being sent
	sendChunkIndexErr := chunkWriter.WriteField("chunkIndex", fmt.Sprintf("%d", chunkIndex))
	if sendChunkIndexErr != nil {
		return sendChunkIndexErr
	}

	// Add the chunk data to the request body
	chunkPart, chunkPartErr := chunkWriter.CreateFormFile("Chunk", fmt.Sprintf("Chunk_%d.chunk", chunkIndex))
	if chunkPartErr != nil {
		return chunkPartErr
	}

	_, writeChunkDataErr := chunkPart.Write(chunkData)
	if writeChunkDataErr != nil {
		return writeChunkDataErr
	}
	//Close the multipart writer to finalize the request body
	if chunkWriterCloseErr := chunkWriter.Close(); chunkWriterCloseErr != nil {
		return chunkWriterCloseErr
	}

	// Send the chunk data to the receiving device
	request, chunkReceiveErr := http.NewRequest(http.MethodPost, "http://"+address+"/chunk", &requestBody)
	if chunkReceiveErr != nil {
		return chunkReceiveErr
	}
	request.Header.Set("Content-Type", chunkWriter.FormDataContentType())

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
