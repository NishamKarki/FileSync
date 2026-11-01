// Author: Rabindra Neupane
package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Rabindra Neupane
// StartChunkTransfer creates an isolated receiver snapshot and returns the
// chunks needed there. Existing comparison-only requests remain read-only.
func StartChunkTransfer(requestContext context.Context, deviceAddress string, metadata FileMetadata) (*TransferPlan, error) {
	if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
		return nil, metadataValidationError
	}
	if metadata.ChunkSize > maxTransferChunkSize {
		return nil, fmt.Errorf("chunk size exceeds 16 MiB transfer limit")
	}
	requestBody, metadataEncodingError := json.Marshal(metadata)
	if metadataEncodingError != nil {
		return nil, metadataEncodingError
	}
	var transferPlan TransferPlan
	transferStartRequestError := transferRequest(requestContext, deviceAddress, "/transfer/start", "application/json", requestBody, &transferPlan)
	if transferStartRequestError != nil {
		return nil, transferStartRequestError
	}
	if transferPlan.TransferID == "" || transferPlan.NeededChunks == nil {
		return nil, fmt.Errorf("invalid transfer plan")
	}
	seenChunkIndexes := make(map[int]bool)
	for _, chunkIndex := range transferPlan.NeededChunks {
		if chunkIndex < 0 || chunkIndex >= len(metadata.ChunkHashes) || seenChunkIndexes[chunkIndex] {
			return nil, fmt.Errorf("invalid needed chunk index")
		}
		seenChunkIndexes[chunkIndex] = true
	}
	return &transferPlan, nil
}

// Rabindra Neupane
// SendTransferChunk stores one chunk in the transfer folder without reconstructing.
func SendTransferChunk(requestContext context.Context, deviceAddress, transferID string, chunkIndex int, chunkData []byte) error {
	queryParameters := url.Values{"transferId": {transferID}, "chunkIndex": {fmt.Sprint(chunkIndex)}}
	return transferRequest(requestContext, deviceAddress, "/transfer/chunk?"+queryParameters.Encode(), "application/octet-stream", chunkData, nil)
}

// Rabindra Neupane
// CompleteChunkTransfer succeeds only after the receiver verifies and reconstructs.
func CompleteChunkTransfer(requestContext context.Context, deviceAddress, transferID, expectedHash string) error {
	requestBody, completionRequestEncodingError := json.Marshal(struct {
		TransferID string `json:"transferId"`
	}{transferID})
	if completionRequestEncodingError != nil {
		return completionRequestEncodingError
	}
	var completionResult TransferCompletion
	transferCompletionRequestError := transferRequest(requestContext, deviceAddress, "/transfer/complete", "application/json", requestBody, &completionResult)
	if transferCompletionRequestError != nil {
		return transferCompletionRequestError
	}
	if completionResult.TransferID == transferID && !completionResult.Completed && completionResult.KeepLocal {
		return &KeepLocalError{}
	}
	if completionResult.TransferID == transferID && !completionResult.Completed && completionResult.ConflictFile != "" {
		return &SyncConflictError{ConflictFile: completionResult.ConflictFile}
	}
	if completionResult.TransferID != transferID || !completionResult.Completed || completionResult.FileHash != expectedHash {
		return fmt.Errorf("receiver did not confirm completion")
	}
	return nil
}

// Rabindra Neupane
// transferRequest sends one bounded transfer request and validates one optional
// JSON response. Redirects are disabled so data cannot be sent to another host.
func transferRequest(requestContext context.Context, deviceAddress, requestPath, contentType string, requestBody []byte, responseResult interface{}) error {
	requestSizeLimit := 8 << 20
	if contentType == "application/octet-stream" {
		requestSizeLimit = maxTransferChunkSize
	}
	if len(requestBody) > requestSizeLimit {
		return fmt.Errorf("transfer request exceeds size limit")
	}
	httpRequest, transferRequestCreationError := http.NewRequestWithContext(requestContext, http.MethodPost, "http://"+deviceAddress+requestPath, bytes.NewReader(requestBody))
	if transferRequestCreationError != nil {
		return transferRequestCreationError
	}
	httpRequest.Header.Set("Content-Type", contentType)
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer httpClient.CloseIdleConnections()
	httpResponse, transferRequestSendingError := httpClient.Do(httpRequest)
	if transferRequestSendingError != nil {
		return fmt.Errorf("transfer request: %w", transferRequestSendingError)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		// Keep the receiver's explanation without accepting an unbounded body.
		const errorLimit = 4096
		errorDetails, receiverErrorReadingError := io.ReadAll(io.LimitReader(httpResponse.Body, errorLimit+1))
		if receiverErrorReadingError != nil {
			return fmt.Errorf("transfer request failed: %s (cannot read receiver error: %v)", httpResponse.Status, receiverErrorReadingError)
		}
		responseTruncated := len(errorDetails) > errorLimit
		if responseTruncated {
			errorDetails = errorDetails[:errorLimit]
		}
		errorMessage := strings.TrimSpace(string(errorDetails))
		if responseTruncated {
			errorMessage += " [truncated]"
		}
		if errorMessage == "" {
			return fmt.Errorf("transfer request failed: %s", httpResponse.Status)
		}
		return fmt.Errorf("transfer request failed: %s: %s", httpResponse.Status, errorMessage)
	}
	if responseResult == nil {
		return nil
	}
	responseData, transferResponseReadingError := io.ReadAll(io.LimitReader(httpResponse.Body, (8<<20)+1))
	if transferResponseReadingError != nil {
		return transferResponseReadingError
	}
	if len(responseData) > 8<<20 {
		return fmt.Errorf("transfer response exceeds size limit")
	}
	responseDecoder := json.NewDecoder(bytes.NewReader(responseData))
	responseDecoder.DisallowUnknownFields()
	if transferResponseDecodingError := responseDecoder.Decode(responseResult); transferResponseDecodingError != nil {
		return transferResponseDecodingError
	}
	if extraTransferResponseDataError := responseDecoder.Decode(&struct{}{}); extraTransferResponseDataError != io.EOF {
		return fmt.Errorf("expected one transfer response")
	}
	return nil
}

// PingDevice sends a ping request to the specified address and returns the response
// It checks whether another FileSync device is reachable on the local network
// The function returns the PingResponse when the device responds
// successfully or an error if the device cannot be reached
func PingDevice(deviceAddress string) (*PingResponse, error) {
	httpTransport := &http.Transport{
		Proxy: nil,
	}

	// Limit the ping request to five seconds so that an offline peer
	// does not leave device discovery waiting forever
	syncClient := http.Client{
		Transport: httpTransport,
		Timeout:   5 * time.Second,
	}
	// Send a GET request to the /ping endpoint of the specified address
	httpResponse, pingRequestError := syncClient.Get("http://" + deviceAddress + "/ping")
	if pingRequestError != nil {
		return nil, pingRequestError
	}

	defer httpResponse.Body.Close()

	// Consider any non-200 response as a failed FileSync transfer.
	if httpResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device returned status: %s", httpResponse.Status)
	}
	// Decode the JSON response into a PingResponse struct
	var pingResponse PingResponse
	pingResponseDecodingError := json.NewDecoder(httpResponse.Body).Decode(&pingResponse)
	if pingResponseDecodingError != nil {
		return nil, pingResponseDecodingError
	}
	return &pingResponse, nil
}

// Rabindra Neupane
// Whole-file comparison keeps one multipart payload but shares conflict handling.
func SendFile(requestContext context.Context, deviceAddress string, metadata FileMetadata, fileData []byte) error {
	if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
		return metadataValidationError
	}
	var requestBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBody)
	encodedMetadata, metadataEncodingError := json.Marshal(metadata)
	if metadataEncodingError != nil {
		return metadataEncodingError
	}
	if multipartMetadataWritingError := multipartWriter.WriteField("metadata", string(encodedMetadata)); multipartMetadataWritingError != nil {
		return multipartMetadataWritingError
	}
	filePart, filePartCreationError := multipartWriter.CreateFormFile("file", metadata.FileName)
	if filePartCreationError != nil {
		return filePartCreationError
	}
	if _, filePartWritingError := filePart.Write(fileData); filePartWritingError != nil {
		return filePartWritingError
	}
	if multipartClosingError := multipartWriter.Close(); multipartClosingError != nil {
		return multipartClosingError
	}
	httpRequest, fileRequestCreationError := http.NewRequestWithContext(requestContext, http.MethodPost, "http://"+deviceAddress+"/file", &requestBody)
	if fileRequestCreationError != nil {
		return fileRequestCreationError
	}
	httpRequest.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	httpClient := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer httpClient.CloseIdleConnections()
	httpResponse, fileRequestSendingError := httpClient.Do(httpRequest)
	if fileRequestSendingError != nil {
		return fileRequestSendingError
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		errorDetails, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return fmt.Errorf("whole-file transfer: %s: %s", httpResponse.Status, strings.TrimSpace(string(errorDetails)))
	}
	var completionResult TransferCompletion
	if completionResponseDecodingError := json.NewDecoder(io.LimitReader(httpResponse.Body, 1<<20)).Decode(&completionResult); completionResponseDecodingError != nil {
		return completionResponseDecodingError
	}
	if completionResult.KeepLocal && !completionResult.Completed {
		return &KeepLocalError{}
	}
	if completionResult.ConflictFile != "" && !completionResult.Completed {
		return &SyncConflictError{ConflictFile: completionResult.ConflictFile}
	}
	if !completionResult.Completed || completionResult.FileHash != metadata.FileHash {
		return fmt.Errorf("receiver did not verify the whole file")
	}
	return nil
}
