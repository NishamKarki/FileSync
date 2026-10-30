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
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", http.MethodPost)
		return fmt.Errorf("only POST requests are allowed")
	}

	request.Body = http.MaxBytesReader(responseWriter, request.Body, 8<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	if requestBodyDecodingError := decoder.Decode(value); requestBodyDecodingError != nil {
		return requestBodyDecodingError
	}
	if extraRequestBodyDataError := decoder.Decode(&struct{}{}); extraRequestBodyDataError != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}

	return nil
}

// Rabindra Neupane
// writeTransferJSON sends a JSON response to the other FileSync device.
func writeTransferJSON(responseWriter http.ResponseWriter, value interface{}) {
	responseWriter.Header().Set("Content-Type", "application/json")
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
	fmt.Printf(
		"Transfer completion failed: id=%q file=%q stage=%q error=%q\n",
		transferID,
		fileName,
		stage,
		transferStageError.Error(),
	)
	http.Error(responseWriter, fmt.Sprintf("%s: %v", stage, transferStageError), statusCode)
}

// Rabindra Neupane
// cacheVerifiedTransfer saves verified chunks only after a transfer is accepted.
func cacheVerifiedTransfer(root, staging string, metadata FileMetadata) error {
	chunkFolder := filepath.Join(root, "File Chunks", metadata.FileName)
	if chunkFolderCreationError := os.MkdirAll(chunkFolder, 0755); chunkFolderCreationError != nil {
		return chunkFolderCreationError
	}

	for chunkIndex := range metadata.ChunkHashes {
		chunkName := fmt.Sprintf("chunk_%d.chunk", chunkIndex)
		stagedChunk := filepath.Join(staging, metadata.FileName, chunkName)
		chunkData, chunkVerificationError := readVerifiedChunk(stagedChunk, metadata, chunkIndex)
		if chunkVerificationError != nil {
			return chunkVerificationError
		}

		storedChunk := filepath.Join(chunkFolder, chunkName)
		if chunkWritingError := os.WriteFile(storedChunk, chunkData, 0644); chunkWritingError != nil {
			return chunkWritingError
		}
	}

	return nil
}
