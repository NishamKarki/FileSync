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
	// Send the metadata and read the receiver's transfer plan.
	var transferPlan TransferPlan
	transferStartRequestError := transferRequest(requestContext, deviceAddress, "/transfer/start", "application/json", requestBody, &transferPlan)
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

// Rabindra Neupane
// SendTransferChunk stores one chunk in the transfer folder without reconstructing.
func SendTransferChunk(requestContext context.Context, deviceAddress, transferID string, chunkIndex int, chunkData []byte) error {
	// Create query values that identify the transfer and chunk position.
	queryParameters := url.Values{"transferId": {transferID}, "chunkIndex": {fmt.Sprint(chunkIndex)}}
	// Send the raw chunk bytes to the receiver's isolated staging folder.
	return transferRequest(requestContext, deviceAddress, "/transfer/chunk?"+queryParameters.Encode(), "application/octet-stream", chunkData, nil)
}

// Rabindra Neupane
// CompleteChunkTransfer succeeds only after the receiver verifies and reconstructs.
func CompleteChunkTransfer(requestContext context.Context, deviceAddress, transferID, expectedHash string) error {
	// Create the completion request for the active transfer.
	requestBody, completionRequestEncodingError := json.Marshal(struct {
		TransferID string `json:"transferId"`
	}{transferID})
	if completionRequestEncodingError != nil {
		return completionRequestEncodingError
	}
	// Send the completion request and decode the receiver's decision.
	var completionResult TransferCompletion
	transferCompletionRequestError := transferRequest(requestContext, deviceAddress, "/transfer/complete", "application/json", requestBody, &completionResult)
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

// Rabindra Neupane
// transferRequest sends one bounded transfer request and validates one optional
// JSON response. Redirects are disabled so data cannot be sent to another host.
func transferRequest(requestContext context.Context, deviceAddress, requestPath, contentType string, requestBody []byte, responseResult interface{}) error {
	// Select a larger limit only for raw chunk data.
	requestSizeLimit := 8 << 20
	if contentType == "application/octet-stream" {
		requestSizeLimit = maxTransferChunkSize
	}
	// Reject a request body that exceeds the selected safety limit.
	if len(requestBody) > requestSizeLimit {
		return fmt.Errorf("transfer request exceeds size limit")
	}
	// Create the POST request for the selected transfer endpoint.
	httpRequest, transferRequestCreationError := http.NewRequestWithContext(requestContext, http.MethodPost, "http://"+deviceAddress+requestPath, bytes.NewReader(requestBody))
	if transferRequestCreationError != nil {
		return transferRequestCreationError
	}
	// Set the body type and create a direct HTTP client with redirects disabled.
	httpRequest.Header.Set("Content-Type", contentType)
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer httpClient.CloseIdleConnections()
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
			return fmt.Errorf("transfer request failed: %s", httpResponse.Status)
		}
		return fmt.Errorf("transfer request failed: %s: %s", httpResponse.Status, errorMessage)
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
// PingDevice checks whether another FileSync device is reachable and returns
// the identity information supplied by its ping endpoint.
func PingDevice(deviceAddress string) (*PingResponse, error) {
	// Create a direct transport so local requests do not use a proxy.
	httpTransport := &http.Transport{
		Proxy: nil,
	}

	// Limit the request so an offline device does not block discovery.
	syncClient := http.Client{
		Transport: httpTransport,
		Timeout:   5 * time.Second,
	}
	// Send the request to the device's ping endpoint.
	httpResponse, pingRequestError := syncClient.Get("http://" + deviceAddress + "/ping")
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
// Whole-file comparison keeps one multipart payload but shares conflict handling.
func SendFile(requestContext context.Context, deviceAddress string, metadata FileMetadata, fileData []byte) error {
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

	// Create the HTTP request for the receiver's whole-file endpoint.
	httpRequest, fileRequestCreationError := http.NewRequestWithContext(requestContext, http.MethodPost, "http://"+deviceAddress+"/file", &requestBody)
	if fileRequestCreationError != nil {
		return fileRequestCreationError
	}

	// Set the multipart content type and create a client without proxy redirects.
	httpRequest.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	httpClient := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer httpClient.CloseIdleConnections()

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
