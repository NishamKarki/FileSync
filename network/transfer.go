// Author: Rabindra Neupane
package network

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Rabindra Neupane
// Keep individual chunks below 16 MiB during network transfer.
const maxTransferChunkSize = 16 << 20

// Rabindra Neupane
// readTransferJSON reads one JSON object from a transfer request.
func readTransferJSON(responseWriter http.ResponseWriter, request *http.Request, value interface{}) error {
	// Validate that this endpoint received the expected request method.
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", http.MethodPost)
		return fmt.Errorf("only POST requests are allowed")
	}

	// Limit the request body before decoding it into memory.
	request.Body = http.MaxBytesReader(responseWriter, request.Body, 8<<20)
	// Create a strict decoder that rejects fields the protocol does not define.
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	// Decode the required JSON object into the caller's value.
	if requestBodyDecodingError := decoder.Decode(value); requestBodyDecodingError != nil {
		return requestBodyDecodingError
	}
	// Reject trailing JSON so one request cannot contain multiple commands.
	if extraRequestBodyDataError := decoder.Decode(&struct{}{}); extraRequestBodyDataError != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}

	// Return success after the complete request body is validated.
	return nil
}

// Rabindra Neupane
// writeTransferJSON sends a JSON response to the other FileSync device.
func writeTransferJSON(responseWriter http.ResponseWriter, value interface{}) {
	// Set the response type before encoding the result.
	responseWriter.Header().Set("Content-Type", "application/json")
	// Encode the response directly to the HTTP connection.
	json.NewEncoder(responseWriter).Encode(value)
}

// Rabindra Neupane
// reportTransferFailure logs the failing stage and returns it to the sender.
func reportTransferFailure(
	responseWriter http.ResponseWriter,
	transferID string,
	fileName string,
	stage string,
	transferStageError error,
	statusCode int,
) {
	// Log enough transfer context to diagnose the failed stage.
	fmt.Printf(
		"Transfer completion failed: id=%q file=%q stage=%q error=%q\n",
		transferID,
		fileName,
		stage,
		transferStageError.Error(),
	)
	// Respond with the same stage and error so the sender can display it.
	http.Error(responseWriter, fmt.Sprintf("%s: %v", stage, transferStageError), statusCode)
}

// Rabindra Neupane
// cacheVerifiedTransfer saves verified chunks only after a transfer is accepted.
func cacheVerifiedTransfer(root, staging string, metadata FileMetadata) error {
	// Create the accepted chunk folder for this file.
	chunkFolder := filepath.Join(root, "File Chunks", metadata.FileName)
	if chunkFolderCreationError := os.MkdirAll(chunkFolder, 0755); chunkFolderCreationError != nil {
		return chunkFolderCreationError
	}

	// Verify and copy every staged chunk into reusable storage.
	for chunkIndex := range metadata.ChunkHashes {
		// Create the expected staged and stored paths for this index.
		chunkName := fmt.Sprintf("chunk_%d.chunk", chunkIndex)
		stagedChunk := filepath.Join(staging, metadata.FileName, chunkName)
		chunkData, chunkVerificationError := readVerifiedChunk(stagedChunk, metadata, chunkIndex)
		// Stop before caching when the staged chunk fails verification.
		if chunkVerificationError != nil {
			return chunkVerificationError
		}

		storedChunk := filepath.Join(chunkFolder, chunkName)
		// Save the verified chunk in the reusable file cache.
		if chunkWritingError := os.WriteFile(storedChunk, chunkData, 0644); chunkWritingError != nil {
			return chunkWritingError
		}
	}

	// Return success after every verified chunk is cached.
	return nil
}
