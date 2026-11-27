// Author: Rabindra Neupane
package network

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DefaultMaxWholeFileUploadBytes caps whole-file multipart uploads at 512 MiB.
const DefaultMaxWholeFileUploadBytes int64 = 512 << 20

// Rabindra Neupane
// NewWholeFileSendHandler receives a complete file for performance comparison over mTLS.
// Normal FileSync synchronization uses the incremental chunk-transfer handler.
func NewWholeFileSendHandler(root string, apply func(FileMetadata, string) error, transferAccepted func(), registry *DeviceRegistry, chunkIndex *ChunkFileIndex) http.Handler {
	return NewWholeFileSendHandlerWithLimit(root, apply, transferAccepted, registry, chunkIndex, DefaultMaxWholeFileUploadBytes)
}

// NewWholeFileSendHandlerWithLimit enforces an explicit max upload byte limit on /file.
func NewWholeFileSendHandlerWithLimit(root string, apply func(FileMetadata, string) error, transferAccepted func(), registry *DeviceRegistry, chunkIndex *ChunkFileIndex, maxUploadBytes int64) http.Handler {
	if maxUploadBytes <= 0 {
		maxUploadBytes = DefaultMaxWholeFileUploadBytes
	}
	// Create the benchmark endpoint without sharing incremental transfer state.
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		// Validate that whole-file uploads use POST.
		if request.Method != http.MethodPost {
			responseWriter.Header().Set("Allow", "POST")
			http.Error(responseWriter, "Only POST requests are allowed", http.StatusMethodNotAllowed)
			return
		}

		// Require verified non-revoked mTLS peer authentication before buffering upload bytes.
		certDeviceID, authErr := authenticatePeerMTLS(registry, request, "")
		if authErr != nil {
			http.Error(responseWriter, authErr.Error(), http.StatusUnauthorized)
			return
		}

		// Bound the entire multipart stream to protect disk and memory from unbounded uploads.
		request.Body = http.MaxBytesReader(responseWriter, request.Body, maxUploadBytes)

		// Create a multipart reader for the metadata and file sections.
		multipartReader, multipartReaderCreationError := request.MultipartReader()
		if multipartReaderCreationError != nil {
			http.Error(responseWriter, "Expected multipart metadata and file", 400)
			return
		}

		// Read the first section and require it to contain transfer metadata.
		metadataPart, metadataPartReadingError := multipartReader.NextPart()
		if metadataPartReadingError != nil || metadataPart.FormName() != "metadata" {
			var maxBytesErr *http.MaxBytesError
			if errors.As(metadataPartReadingError, &maxBytesErr) {
				http.Error(responseWriter, "Whole-file upload exceeds maximum allowed size", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(responseWriter, "Transfer metadata is required; update both peers", 400)
			return
		}

		// Limit the metadata size before reading it into memory.
		metadataData, metadataReadingError := io.ReadAll(io.LimitReader(metadataPart, (8<<20)+1))
		if metadataReadingError != nil || len(metadataData) > 8<<20 {
			var maxBytesErr *http.MaxBytesError
			if errors.As(metadataReadingError, &maxBytesErr) {
				http.Error(responseWriter, "Whole-file upload exceeds maximum allowed size", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(responseWriter, "Invalid transfer metadata", 400)
			return
		}

		// Decode one metadata object and reject unknown fields.
		var metadata FileMetadata
		metadataDecoder := json.NewDecoder(bytes.NewReader(metadataData))
		metadataDecoder.DisallowUnknownFields()
		if metadataDecodingError := metadataDecoder.Decode(&metadata); metadataDecodingError != nil {
			http.Error(responseWriter, "Invalid transfer metadata", 400)
			return
		}

		// Check that no second metadata object was included.
		if extraMetadataError := metadataDecoder.Decode(&struct{}{}); extraMetadataError != io.EOF {
			http.Error(responseWriter, "Expected one metadata object", 400)
			return
		}

		// Validate the file name, size, hashes, and other transfer values.
		if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
			http.Error(responseWriter, metadataValidationError.Error(), 400)
			return
		}
		if metadata.DeviceID != certDeviceID {
			http.Error(responseWriter, "Sender device identity does not match authenticated mTLS certificate", http.StatusUnauthorized)
			return
		}
		if metadata.FileSize > maxUploadBytes {
			http.Error(responseWriter, "Declared file size exceeds whole-file upload limit", http.StatusRequestEntityTooLarge)
			return
		}

		// Reject chunk sizes that exceed the receiver's safety limit.
		if metadata.ChunkSize > maxTransferChunkSize {
			http.Error(responseWriter, "Chunk size exceeds transfer limit", 400)
			return
		}

		// Read the file section and confirm that its name matches the metadata.
		filePart, filePartReadingError := multipartReader.NextPart()
		if filePartReadingError != nil || filePart.FormName() != "file" || filePart.FileName() != metadata.FileName {
			var maxBytesErr *http.MaxBytesError
			if errors.As(filePartReadingError, &maxBytesErr) {
				http.Error(responseWriter, "Whole-file upload exceeds maximum allowed size", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(responseWriter, "Expected matching file part", 400)
			return
		}

		// Create the shared staging directory for incoming transfers.
		staging := filepath.Join(root, "File Chunks", ".transfers")
		if stagingDirectoryCreationError := os.MkdirAll(staging, 0755); stagingDirectoryCreationError != nil {
			http.Error(responseWriter, "Cannot create transfer storage", 500)
			return
		}

		// Create an isolated folder for this incoming file.
		transferRoot, transferDirectoryCreationError := os.MkdirTemp(staging, strings.ReplaceAll(metadata.FileName, "*", "_")+"-incoming-")
		if transferDirectoryCreationError != nil {
			http.Error(responseWriter, "Cannot create transfer folder", 500)
			return
		}
		defer os.RemoveAll(transferRoot)

		// Create the file-specific folder that stores verified chunks.
		folder := filepath.Join(transferRoot, metadata.FileName)
		if chunkDirectoryCreationError := os.Mkdir(folder, 0755); chunkDirectoryCreationError != nil {
			http.Error(responseWriter, "Cannot create chunk folder", 500)
			return
		}

		// Create a whole-file hasher and track how many bytes remain.
		hasher := sha256.New()
		remaining := metadata.FileSize

		// Read, verify, and stage every chunk in its original order.
		for index, expected := range metadata.ChunkHashes {
			// Use the remaining byte count for the final smaller chunk.
			size := metadata.ChunkSize
			if remaining < size {
				size = remaining
			}

			// Read exactly the number of bytes expected for this chunk.
			chunk := make([]byte, int(size))
			if _, chunkReadingError := io.ReadFull(filePart, chunk); chunkReadingError != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(chunkReadingError, &maxBytesErr) {
					http.Error(responseWriter, "Whole-file upload exceeds maximum allowed size", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(responseWriter, "Incomplete uploaded file", 400)
				return
			}

			// Verify the chunk before saving it to staging.
			sum := sha256.Sum256(chunk)
			if hex.EncodeToString(sum[:]) != expected {
				http.Error(responseWriter, "Uploaded chunk hash mismatch", 409)
				return
			}

			// Save the verified chunk using its ordered index.
			if chunkWritingError := os.WriteFile(filepath.Join(folder, fmt.Sprintf("chunk_%d.chunk", index)), chunk, 0644); chunkWritingError != nil {
				http.Error(responseWriter, "Cannot stage uploaded chunk", 500)
				return
			}

			// Add the chunk to the whole-file hash and update the remaining size.
			hasher.Write(chunk)
			remaining -= size
		}

		// Check that the uploaded file contains no extra bytes.
		var extra [1]byte
		if extraByteCount, extraFileDataError := filePart.Read(extra[:]); extraByteCount != 0 || extraFileDataError != io.EOF {
			http.Error(responseWriter, "Uploaded size mismatch", 400)
			return
		}

		// Verify the complete file hash after all chunks pass verification.
		if hex.EncodeToString(hasher.Sum(nil)) != metadata.FileHash {
			http.Error(responseWriter, "Uploaded file hash mismatch", 409)
			return
		}

		// Check that the request contains no unexpected multipart sections.
		if _, extraMultipartPartError := multipartReader.NextPart(); extraMultipartPartError != io.EOF {
			http.Error(responseWriter, "Unexpected multipart content", 400)
			return
		}

		// Check that the application callback is available.
		if apply == nil {
			http.Error(responseWriter, "File application unavailable", 503)
			return
		}

		// Apply the verified file and return known conflict decisions as JSON.
		if verifiedFileApplyError := apply(metadata, transferRoot); verifiedFileApplyError != nil {
			var keepLocalError *KeepLocalError
			var syncConflictError *SyncConflictError
			// Tell the sender when the receiver keeps its newer local file.
			if errors.As(verifiedFileApplyError, &keepLocalError) {
				writeTransferJSON(responseWriter, TransferCompletion{KeepLocal: true})
				return
			}
			// Tell the sender where the incoming conflict copy was preserved.
			if errors.As(verifiedFileApplyError, &syncConflictError) {
				writeTransferJSON(responseWriter, TransferCompletion{ConflictFile: syncConflictError.ConflictFile})
				return
			}
			// Return an internal error for any unexpected apply failure.
			http.Error(responseWriter, fmt.Sprintf("Apply verified file: %v", verifiedFileApplyError), 500)
			return
		}

		// Cache the verified chunks for later incremental comparisons and update index.
		if verifiedChunkCachingError := cacheVerifiedTransfer(root, transferRoot, metadata, chunkIndex); verifiedChunkCachingError != nil {
			http.Error(responseWriter, fmt.Sprintf("Cache verified chunks: %v", verifiedChunkCachingError), 500)
			return
		}

		// Report the accepted cache after every verified benchmark transfer.
		if transferAccepted != nil {
			go transferAccepted()
		}

		// Respond with the verified file hash after the transfer completes.
		writeTransferJSON(responseWriter, TransferCompletion{Completed: true, FileHash: metadata.FileHash})
	})
}
