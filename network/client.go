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
	"net/url"
	"strings"
	"time"
)

//Rabindra Neupane
// StartChunkTransfer creates an isolated receiver snapshot and returns the
// chunks needed there. Existing comparison-only requests remain read-only.
func StartChunkTransfer(ctx context.Context, address string, metadata FileMetadata) (*TransferPlan, error) {
	if err := validateFileMetadata(metadata); err != nil {
		return nil, err
	}
	if metadata.ChunkSize > maxTransferChunkSize {
		return nil, fmt.Errorf("chunk size exceeds 16 MiB transfer limit")
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	var result TransferPlan
	if err := transferRequest(ctx, address, "/transfer/start", "application/json", body, &result); err != nil {
		return nil, err
	}
	if result.TransferID == "" || result.NeededChunks == nil {
		return nil, fmt.Errorf("invalid transfer plan")
	}
	seen := make(map[int]bool)
	for _, index := range result.NeededChunks {
		if index < 0 || index >= len(metadata.ChunkHashes) || seen[index] {
			return nil, fmt.Errorf("invalid needed chunk index")
		}
		seen[index] = true
	}
	return &result, nil
}

//Rabindra Neupane
// SendTransferChunk stores one chunk in the transfer folder without reconstructing.
func SendTransferChunk(ctx context.Context, address, transferID string, index int, data []byte) error {
	query := url.Values{"transferId": {transferID}, "chunkIndex": {fmt.Sprint(index)}}
	return transferRequest(ctx, address, "/transfer/chunk?"+query.Encode(), "application/octet-stream", data, nil)
}

//Rabindra Neupane
// CompleteChunkTransfer succeeds only after the receiver verifies and reconstructs.
func CompleteChunkTransfer(ctx context.Context, address, transferID, expectedHash string) error {
	body, err := json.Marshal(struct {
		TransferID string `json:"transferId"`
	}{transferID})
	if err != nil {
		return err
	}
	var result TransferCompletion
	if err := transferRequest(ctx, address, "/transfer/complete", "application/json", body, &result); err != nil {
		return err
	}
	//Rabindra Neupane
	if result.TransferID == transferID && !result.Completed && result.KeepLocal {
		return &KeepLocalError{}
	}
	if result.TransferID == transferID && !result.Completed && result.ConflictFile != "" {
		return &SyncConflictError{ConflictFile: result.ConflictFile}
	}
	if result.TransferID != transferID || !result.Completed || result.FileHash != expectedHash {
		return fmt.Errorf("receiver did not confirm completion")
	}
	return nil
}

//Rabindra Neupane
func transferRequest(ctx context.Context, address, path, contentType string, body []byte, result interface{}) error {
	limit := 8 << 20
	if contentType == "application/octet-stream" {
		limit = maxTransferChunkSize
	}
	if len(body) > limit {
		return fmt.Errorf("transfer request exceeds size limit")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("transfer request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		//Rabindra Neupane
		// Keep the receiver's explanation without accepting an unbounded body.
		const errorLimit = 4096
		detail, readErr := io.ReadAll(io.LimitReader(response.Body, errorLimit+1))
		if readErr != nil {
			return fmt.Errorf("transfer request failed: %s (cannot read receiver error: %v)", response.Status, readErr)
		}
		truncated := len(detail) > errorLimit
		if truncated {
			detail = detail[:errorLimit]
		}
		message := strings.TrimSpace(string(detail))
		if truncated {
			message += " [truncated]"
		}
		if message == "" {
			return fmt.Errorf("transfer request failed: %s", response.Status)
		}
		return fmt.Errorf("transfer request failed: %s: %s", response.Status, message)
	}
	if result == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("transfer response exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("expected one transfer response")
	}
	return nil
}

// CompareFileMetadata asks a peer which chunks it needs. The address uses the
// host:port format used by file transfers. This does not send file content or confirm
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

//Rabindra Neupane
// Whole-file comparison keeps one multipart payload but shares conflict handling.
func SendFile(ctx context.Context, address string, metadata FileMetadata, data []byte) error {
	if err := validateFileMetadata(metadata); err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if err := writer.WriteField("metadata", string(encoded)); err != nil {
		return err
	}
	part, err := writer.CreateFormFile("file", metadata.FileName)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/file", &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("whole-file transfer: %s: %s", response.Status, strings.TrimSpace(string(detail)))
	}
	var result TransferCompletion
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return err
	}
	if result.KeepLocal && !result.Completed {
		return &KeepLocalError{}
	}
	if result.ConflictFile != "" && !result.Completed {
		return &SyncConflictError{ConflictFile: result.ConflictFile}
	}
	if !result.Completed || result.FileHash != metadata.FileHash {
		return fmt.Errorf("receiver did not verify the whole file")
	}
	return nil
}
