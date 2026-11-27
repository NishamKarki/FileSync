// Author: Rabindra Neupane
package network

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Santosh Tripathee
// Reuse TCP/TLS connections so large transfers do not exhaust local network ports.
var transferHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	},
	Timeout: 2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func selectTransferClient(clients []*http.Client) *http.Client {
	if len(clients) > 0 && clients[0] != nil {
		return clients[0]
	}
	return transferHTTPClient
}

// Rabindra Neupane
// StartChunkTransfer creates an isolated receiver snapshot and returns the
// chunks needed there. Existing comparison-only requests remain read-only.
func StartChunkTransfer(requestContext context.Context, deviceAddress string, metadata FileMetadata, clients ...*http.Client) (*TransferPlan, error) {
	// Validate the metadata before sending it to another device.
	if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
		return nil, metadataValidationError
	}
	// Check the declared chunk size before creating the request body.
	if metadata.ChunkSize > maxTransferChunkSize {
		return nil, fmt.Errorf("chunk size exceeds 16 MiB transfer limit")
	}
	// Create the JSON request used to start the transfer.
	requestBody, metadataEncodingError := json.Marshal(metadata)
	if metadataEncodingError != nil {
		return nil, metadataEncodingError
	}
	// Allow cached-chunk verification more time than one small chunk upload.
	startContext, cancelStart := context.WithTimeout(requestContext, time.Minute)
	defer cancelStart()
	// Send the metadata and read the receiver's transfer plan.
	var transferPlan TransferPlan
	httpClient := selectTransferClient(clients)
	transferStartRequestError := transferRequestWithClient(startContext, httpClient, deviceAddress, "/transfer/start", "application/json", requestBody, &transferPlan)
	if transferStartRequestError != nil {
		return nil, transferStartRequestError
	}
	// Validate the transfer ID and missing-chunk list returned by the receiver.
	if transferPlan.TransferID == "" || transferPlan.NeededChunks == nil {
		return nil, fmt.Errorf("invalid transfer plan")
	}
	// Check every requested index once so a bad response cannot read the wrong chunk.
	seenChunkIndexes := make(map[int]bool)
	for _, chunkIndex := range transferPlan.NeededChunks {
		// Reject an out-of-range or repeated chunk index.
		if chunkIndex < 0 || chunkIndex >= len(metadata.ChunkHashes) || seenChunkIndexes[chunkIndex] {
			return nil, fmt.Errorf("invalid needed chunk index")
		}
		seenChunkIndexes[chunkIndex] = true
	}
	// Return the receiver's validated transfer plan.
	return &transferPlan, nil
}

// httpStatusError captures a non-200 HTTP response so transient or hash-mismatch
// chunk uploads can be retried by SendTransferChunk.
type httpStatusError struct {
	StatusCode int
	Message    string
}

func (e *httpStatusError) Error() string {
	return e.Message
}

// Rabindra Neupane
// SendTransferChunk stores one chunk in the transfer folder without reconstructing.
func SendTransferChunk(requestContext context.Context, deviceAddress, transferID string, chunkIndex int, chunkData []byte, clients ...*http.Client) error {
	// Create query values that identify the transfer and chunk position.
	queryParameters := url.Values{"transferId": {transferID}, "chunkIndex": {fmt.Sprint(chunkIndex)}}
	requestPath := "/transfer/chunk?" + queryParameters.Encode()
	httpClient := selectTransferClient(clients)
	// Retry transient network failures, server errors, or in-transit chunk corruption (400).
	var lastChunkSendingError error
	for attempt := 0; attempt < 3; attempt++ {
		// Bound one chunk attempt without shortening the overall transfer context.
		chunkContext, cancelChunk := context.WithTimeout(requestContext, 15*time.Second)
		lastChunkSendingError = transferRequestWithClient(chunkContext, httpClient, deviceAddress, requestPath, "application/octet-stream", chunkData, nil)
		cancelChunk()
		if lastChunkSendingError == nil {
			return nil
		}
		var networkError net.Error
		var statusError *httpStatusError
		retryable := errors.As(lastChunkSendingError, &networkError) ||
			(errors.As(lastChunkSendingError, &statusError) && (statusError.StatusCode == http.StatusBadRequest || statusError.StatusCode >= http.StatusInternalServerError))
		if !retryable {
			return lastChunkSendingError
		}
		if attempt == 2 {
			break
		}
		// Stop immediately when the caller cancels the complete transfer.
		retryDelay := time.Duration(attempt+1) * 250 * time.Millisecond
		retryTimer := time.NewTimer(retryDelay)
		select {
		case <-requestContext.Done():
			retryTimer.Stop()
			return requestContext.Err()
		case <-retryTimer.C:
		}
	}
	return fmt.Errorf("chunk request failed after 3 attempts: %w", lastChunkSendingError)
}

// Rabindra Neupane
// CompleteChunkTransfer succeeds only after the receiver verifies and reconstructs.
func CompleteChunkTransfer(requestContext context.Context, deviceAddress, transferID, expectedHash string, clients ...*http.Client) error {
	// Create the completion request for the active transfer.
	requestBody, completionRequestEncodingError := json.Marshal(struct {
		TransferID string `json:"transferId"`
	}{transferID})
	if completionRequestEncodingError != nil {
		return completionRequestEncodingError
	}
	// Allow final verification, reconstruction, and conflict handling to finish.
	completionContext, cancelCompletion := context.WithTimeout(requestContext, 2*time.Minute)
	defer cancelCompletion()
	// Send the completion request and decode the receiver's decision.
	var completionResult TransferCompletion
	httpClient := selectTransferClient(clients)
	transferCompletionRequestError := transferRequestWithClient(completionContext, httpClient, deviceAddress, "/transfer/complete", "application/json", requestBody, &completionResult)
	if transferCompletionRequestError != nil {
		return transferCompletionRequestError
	}
	// Respond to a newer receiver version without treating it as data loss.
	if completionResult.TransferID == transferID && !completionResult.Completed && completionResult.KeepLocal {
		return &KeepLocalError{}
	}
	// Report a preserved conflict copy when both devices changed the file.
	if completionResult.TransferID == transferID && !completionResult.Completed && completionResult.ConflictFile != "" {
		return &SyncConflictError{ConflictFile: completionResult.ConflictFile}
	}
	// Verify the transfer ID, completion flag, and final file hash.
	if completionResult.TransferID != transferID || !completionResult.Completed || completionResult.FileHash != expectedHash {
		return fmt.Errorf("receiver did not confirm completion")
	}
	// Return success after the receiver confirms the expected file.
	return nil
}

func transferRequestWithClient(requestContext context.Context, httpClient *http.Client, deviceAddress, requestPath, contentType string, requestBody []byte, responseResult interface{}) error {
	// Select a larger limit only for raw chunk data.
	requestSizeLimit := 8 << 20
	if contentType == "application/octet-stream" {
		requestSizeLimit = maxTransferChunkSize
	}
	// Reject a request body that exceeds the selected safety limit.
	if len(requestBody) > requestSizeLimit {
		return fmt.Errorf("transfer request exceeds size limit")
	}
	// Create the HTTPS POST request for the selected transfer endpoint.
	httpRequest, transferRequestCreationError := http.NewRequestWithContext(requestContext, http.MethodPost, "https://"+deviceAddress+requestPath, bytes.NewReader(requestBody))
	if transferRequestCreationError != nil {
		return transferRequestCreationError
	}
	// Set the body type before using the selected HTTPS transfer client.
	httpRequest.Header.Set("Content-Type", contentType)
	// Send the transfer request to the receiver.
	httpResponse, transferRequestSendingError := httpClient.Do(httpRequest)
	if transferRequestSendingError != nil {
		return fmt.Errorf("transfer request: %w", transferRequestSendingError)
	}
	defer httpResponse.Body.Close()
	// Read and return a bounded error when the receiver rejects the request.
	if httpResponse.StatusCode != http.StatusOK {
		// Read a bounded error message so the receiver can explain the failure safely.
		const errorLimit = 4096
		errorDetails, receiverErrorReadingError := io.ReadAll(io.LimitReader(httpResponse.Body, errorLimit+1))
		if receiverErrorReadingError != nil {
			return fmt.Errorf("transfer request failed: %s (cannot read receiver error: %v)", httpResponse.Status, receiverErrorReadingError)
		}
		// Mark an error message that was longer than the safe reading limit.
		responseTruncated := len(errorDetails) > errorLimit
		if responseTruncated {
			errorDetails = errorDetails[:errorLimit]
		}
		// Remove surrounding whitespace from the receiver's message.
		errorMessage := strings.TrimSpace(string(errorDetails))
		if responseTruncated {
			errorMessage += " [truncated]"
		}
		// Return the HTTP status alone when no details were provided.
		if errorMessage == "" {
			return &httpStatusError{StatusCode: httpResponse.StatusCode, Message: fmt.Sprintf("transfer request failed: %s", httpResponse.Status)}
		}
		return &httpStatusError{StatusCode: httpResponse.StatusCode, Message: fmt.Sprintf("transfer request failed: %s: %s", httpResponse.Status, errorMessage)}
	}
	// Return when the caller does not expect a JSON response.
	if responseResult == nil {
		return nil
	}
	// Read the bounded JSON response into memory.
	responseData, transferResponseReadingError := io.ReadAll(io.LimitReader(httpResponse.Body, (8<<20)+1))
	if transferResponseReadingError != nil {
		return transferResponseReadingError
	}
	// Reject a response that exceeds the allowed JSON size.
	if len(responseData) > 8<<20 {
		return fmt.Errorf("transfer response exceeds size limit")
	}
	// Decode one response object and reject unknown fields.
	responseDecoder := json.NewDecoder(bytes.NewReader(responseData))
	responseDecoder.DisallowUnknownFields()
	if transferResponseDecodingError := responseDecoder.Decode(responseResult); transferResponseDecodingError != nil {
		return transferResponseDecodingError
	}
	// Check that the response contains only one JSON object.
	if extraTransferResponseDataError := responseDecoder.Decode(&struct{}{}); extraTransferResponseDataError != io.EOF {
		return fmt.Errorf("expected one transfer response")
	}
	// Return success after the complete response is validated.
	return nil
}

// Rabindra Neupane
// PingDevice checks whether another FileSync device is reachable over HTTPS
// and returns the public identity and CA fingerprint supplied by /ping.
func PingDevice(deviceAddress string, clients ...*http.Client) (*PingResponse, error) {
	var syncClient *http.Client
	if len(clients) > 0 && clients[0] != nil {
		syncClient = clients[0]
	} else {
		syncClient = bootstrapTLSHTTPClient("", 5*time.Second, nil)
		defer syncClient.CloseIdleConnections()
	}

	// Send the request to the device's HTTPS ping endpoint.
	httpResponse, pingRequestError := syncClient.Get("https://" + deviceAddress + "/ping")
	if pingRequestError != nil {
		return nil, pingRequestError
	}

	defer httpResponse.Body.Close()

	// Check that the endpoint accepted the request.
	if httpResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device returned status: %s", httpResponse.Status)
	}
	// Read the returned device information.
	var pingResponse PingResponse
	pingResponseDecodingError := json.NewDecoder(httpResponse.Body).Decode(&pingResponse)
	if pingResponseDecodingError != nil {
		return nil, pingResponseDecodingError
	}
	return &pingResponse, nil
}

// Rabindra Neupane
// Whole-file comparison keeps one multipart payload over mTLS and shares conflict handling.
func SendFile(requestContext context.Context, deviceAddress string, metadata FileMetadata, fileData []byte, clients ...*http.Client) error {
	// Validate the same metadata used by incremental transfers.
	if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
		return metadataValidationError
	}
	// Create the multipart request body used to carry metadata and file data.
	var requestBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBody)

	// Encode the file metadata before adding it to the request.
	encodedMetadata, metadataEncodingError := json.Marshal(metadata)
	if metadataEncodingError != nil {
		return metadataEncodingError
	}

	// Add the encoded metadata as the first multipart field.
	if multipartMetadataWritingError := multipartWriter.WriteField("metadata", string(encodedMetadata)); multipartMetadataWritingError != nil {
		return multipartMetadataWritingError
	}

	// Create the file section using the name recorded in the metadata.
	filePart, filePartCreationError := multipartWriter.CreateFormFile("file", metadata.FileName)
	if filePartCreationError != nil {
		return filePartCreationError
	}

	// Write the complete file data into the multipart file section.
	if _, filePartWritingError := filePart.Write(fileData); filePartWritingError != nil {
		return filePartWritingError
	}

	// Close the multipart writer so its final boundary is added.
	if multipartClosingError := multipartWriter.Close(); multipartClosingError != nil {
		return multipartClosingError
	}

	// Create the HTTPS request for the receiver's whole-file endpoint.
	httpRequest, fileRequestCreationError := http.NewRequestWithContext(requestContext, http.MethodPost, "https://"+deviceAddress+"/file", &requestBody)
	if fileRequestCreationError != nil {
		return fileRequestCreationError
	}

	// Set the multipart content type and send through the configured mTLS client.
	httpRequest.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	httpClient := selectTransferClient(clients)

	// Send the request to the receiving device.
	httpResponse, fileRequestSendingError := httpClient.Do(httpRequest)
	if fileRequestSendingError != nil {
		return fileRequestSendingError
	}
	defer httpResponse.Body.Close()

	// Read a short error response when the receiver rejects the transfer.
	if httpResponse.StatusCode != http.StatusOK {
		errorDetails, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return fmt.Errorf("whole-file transfer: %s: %s", httpResponse.Status, strings.TrimSpace(string(errorDetails)))
	}

	// Decode the receiver's verification result.
	var completionResult TransferCompletion
	if completionResponseDecodingError := json.NewDecoder(io.LimitReader(httpResponse.Body, 1<<20)).Decode(&completionResult); completionResponseDecodingError != nil {
		return completionResponseDecodingError
	}

	// Report when the receiver kept its newer local version.
	if completionResult.KeepLocal && !completionResult.Completed {
		return &KeepLocalError{}
	}

	// Report the saved conflict copy when both devices changed the file.
	if completionResult.ConflictFile != "" && !completionResult.Completed {
		return &SyncConflictError{ConflictFile: completionResult.ConflictFile}
	}

	// Verify that the receiver completed the transfer with the expected hash.
	if !completionResult.Completed || completionResult.FileHash != metadata.FileHash {
		return fmt.Errorf("receiver did not verify the whole file")
	}

	// Return success after the receiver verifies the complete file.
	return nil
}

// FetchFileMetadata requests the latest file metadata from a peer over mTLS for pull synchronization.
func FetchFileMetadata(requestContext context.Context, deviceAddress, fileName string, clients ...*http.Client) (FileMetadata, error) {
	requestURL := "https://" + deviceAddress + "/files/metadata?name=" + url.QueryEscape(fileName)
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodGet, requestURL, nil)
	if err != nil {
		return FileMetadata{}, err
	}
	httpResponse, err := selectTransferClient(clients).Do(httpRequest)
	if err != nil {
		return FileMetadata{}, err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		details, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return FileMetadata{}, fmt.Errorf("metadata request failed: %s: %s", httpResponse.Status, strings.TrimSpace(string(details)))
	}
	var metadata FileMetadata
	if err := json.NewDecoder(io.LimitReader(httpResponse.Body, 8<<20)).Decode(&metadata); err != nil {
		return FileMetadata{}, err
	}
	if err := validateFileMetadata(metadata); err != nil {
		return FileMetadata{}, err
	}
	return metadata, nil
}

// FetchChunkFromAddress downloads and SHA-256 verifies one chunk from a peer over mTLS.
func FetchChunkFromAddress(requestContext context.Context, deviceAddress, chunkHash string, clients ...*http.Client) ([]byte, error) {
	if !validLocationHash(chunkHash) {
		return nil, fmt.Errorf("invalid chunk hash")
	}
	requestURL := "https://" + deviceAddress + "/chunks/data?hash=" + url.QueryEscape(chunkHash)
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	httpResponse, err := selectTransferClient(clients).Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		details, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return nil, fmt.Errorf("chunk fetch failed: %s: %s", httpResponse.Status, strings.TrimSpace(string(details)))
	}
	data, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxTransferChunkSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTransferChunkSize {
		return nil, fmt.Errorf("chunk exceeds transfer limit")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != chunkHash {
		return nil, fmt.Errorf("downloaded chunk SHA-256 mismatch")
	}
	return data, nil
}
