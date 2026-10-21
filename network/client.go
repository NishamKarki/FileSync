// /Rabindra Neupane
package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// CompareFileMetadata asks a peer which chunks it needs. The address uses the
// same host:port format as SendChunk. This does not send file content or confirm
// a completed synchronization. The context lets callers cancel the request.
//Rabindra Neupane
func CompareFileMetadata(ctx context.Context, address string, metadata FileMetadata) (*FileMetadataComparison, error) {
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   30 * time.Second,
		// Metadata must be compared by the selected peer, without redirects.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	return requestFileMetadata(ctx, client, address, metadata)
}

//Rabindra Neupane
func requestFileMetadata(ctx context.Context, client *http.Client, address string, metadata FileMetadata) (*FileMetadataComparison, error) {
	if err := validateFileMetadata(metadata); err != nil {
		return nil, fmt.Errorf("invalid file metadata: %w", err)
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode file metadata: %w", err)
	}
	if len(body) > 8<<20 {
		return nil, fmt.Errorf("file metadata exceeds the receiver's 8 MiB limit")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/file-metadata", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create file metadata request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("compare file metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("file metadata comparison failed: %s", response.Status)
	}
	// Bound the response before decoding, including any trailing data.
	const responseLimit = 8 << 20
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read file metadata response: %w", err)
	}
	if len(responseBody) > responseLimit {
		return nil, fmt.Errorf("file metadata response exceeds 8 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	var result FileMetadataComparison
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid file metadata response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("expected one file metadata response")
	}
	// Missing/null lists must not be mistaken for 'no chunks needed'.
	if result.NeededChunks == nil {
		return nil, fmt.Errorf("file metadata response is missing neededChunks")
	}
	seen := make(map[int]bool, len(result.NeededChunks))
	for _, index := range result.NeededChunks {
		if index < 0 || index >= len(metadata.ChunkHashes) || seen[index] {
			return nil, fmt.Errorf("file metadata response contains an invalid or duplicate chunk index: %d", index)
		}
		seen[index] = true
	}
	return &result, nil
}

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
//Rabindra Neupane
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
	if _, err := io.Copy(filePart, fileToSend); err != nil {
		return err
	}

	// Close the multipart writer to finalize the request body
	if err := fileWriter.Close(); err != nil {
		return err
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
//Rabindra Neupane
func SendChunk(address string, fileName string, chunkIndex int, totalChunks int, chunkHash string, chunkData []byte) error {
	var requestBody bytes.Buffer
	chunkWriter := multipart.NewWriter(&requestBody)

	// Send the name of the original file being split into chunks
	if err := chunkWriter.WriteField("fileName", fileName); err != nil {
		return err
	}

	// Send the index of the current chunk being sent
	if err := chunkWriter.WriteField("chunkIndex", fmt.Sprintf("%d", chunkIndex)); err != nil {
		return err
	}

	// Send the total number of chunks being sent
	if err := chunkWriter.WriteField("totalChunks", fmt.Sprintf("%d", totalChunks)); err != nil {
		return err
	}

	// Send the SHA-256 hash of the chunk so the receiving device can verify its integrity
	if err := chunkWriter.WriteField("chunkHash", chunkHash); err != nil {
		return err
	}

	// Add the chunk data to the request body
	chunkPart, chunkPartCreationError := chunkWriter.CreateFormFile("Chunk", fmt.Sprintf("Chunk_%d.chunk", chunkIndex))
	if chunkPartCreationError != nil {
		return chunkPartCreationError
	}

	if _, err := chunkPart.Write(chunkData); err != nil {
		return err
	}

	if err := chunkWriter.Close(); err != nil {
		return err
	}

	// Send the chunk data to the receiving device
	request, requestCreationError := http.NewRequest(http.MethodPost, "http://"+address+"/chunk", &requestBody)
	if requestCreationError != nil {
		return requestCreationError
	}
	request.Header.Set("Content-Type", chunkWriter.FormDataContentType())

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	response, requestSendError := client.Do(request)
	if requestSendError != nil {
		return requestSendError
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
