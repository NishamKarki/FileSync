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

// Rabindra Neupane
// NewWholeFileSendHandler receives a complete file for performance comparison.
// Normal FileSync synchronization uses the incremental chunk-transfer handler.
func NewWholeFileSendHandler(root string, apply func(FileMetadata, string) error) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			responseWriter.Header().Set("Allow", "POST")
			http.Error(responseWriter, "Only POST requests are allowed", http.StatusMethodNotAllowed)
			return
		}
		multipartReader, multipartReaderCreationError := request.MultipartReader()
		if multipartReaderCreationError != nil {
			http.Error(responseWriter, "Expected multipart metadata and file", 400)
			return
		}
		metadataPart, metadataPartReadingError := multipartReader.NextPart()
		if metadataPartReadingError != nil || metadataPart.FormName() != "metadata" {
			http.Error(responseWriter, "Transfer metadata is required; update both peers", 400)
			return
		}
		metadataData, metadataReadingError := io.ReadAll(io.LimitReader(metadataPart, (8<<20)+1))
		if metadataReadingError != nil || len(metadataData) > 8<<20 {
			http.Error(responseWriter, "Invalid transfer metadata", 400)
			return
		}
		var metadata FileMetadata
		metadataDecoder := json.NewDecoder(bytes.NewReader(metadataData))
		metadataDecoder.DisallowUnknownFields()
		if metadataDecodingError := metadataDecoder.Decode(&metadata); metadataDecodingError != nil {
			http.Error(responseWriter, "Invalid transfer metadata", 400)
			return
		}
		if extraMetadataError := metadataDecoder.Decode(&struct{}{}); extraMetadataError != io.EOF {
			http.Error(responseWriter, "Expected one metadata object", 400)
			return
		}
		if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
			http.Error(responseWriter, metadataValidationError.Error(), 400)
			return
		}
		if metadata.ChunkSize > maxTransferChunkSize {
			http.Error(responseWriter, "Chunk size exceeds transfer limit", 400)
			return
		}
		filePart, filePartReadingError := multipartReader.NextPart()
		if filePartReadingError != nil || filePart.FormName() != "file" || filePart.FileName() != metadata.FileName {
			http.Error(responseWriter, "Expected matching file part", 400)
			return
		}
		staging := filepath.Join(root, "File Chunks", ".transfers")
		if stagingDirectoryCreationError := os.MkdirAll(staging, 0755); stagingDirectoryCreationError != nil {
			http.Error(responseWriter, "Cannot create transfer storage", 500)
			return
		}
		transferRoot, transferDirectoryCreationError := os.MkdirTemp(staging, strings.ReplaceAll(metadata.FileName, "*", "_")+"-incoming-")
		if transferDirectoryCreationError != nil {
			http.Error(responseWriter, "Cannot create transfer folder", 500)
			return
		}
		folder := filepath.Join(transferRoot, metadata.FileName)
		if chunkDirectoryCreationError := os.Mkdir(folder, 0755); chunkDirectoryCreationError != nil {
			http.Error(responseWriter, "Cannot create chunk folder", 500)
			return
		}
		hasher := sha256.New()
		remaining := metadata.FileSize
		for index, expected := range metadata.ChunkHashes {
			size := metadata.ChunkSize
			if remaining < size {
				size = remaining
			}
			chunk := make([]byte, int(size))
			if _, chunkReadingError := io.ReadFull(filePart, chunk); chunkReadingError != nil {
				http.Error(responseWriter, "Incomplete uploaded file", 400)
				return
			}
			sum := sha256.Sum256(chunk)
			if hex.EncodeToString(sum[:]) != expected {
				http.Error(responseWriter, "Uploaded chunk hash mismatch", 409)
				return
			}
			if chunkWritingError := os.WriteFile(filepath.Join(folder, fmt.Sprintf("chunk_%d.chunk", index)), chunk, 0644); chunkWritingError != nil {
				http.Error(responseWriter, "Cannot stage uploaded chunk", 500)
				return
			}
			hasher.Write(chunk)
			remaining -= size
		}
		var extra [1]byte
		if extraByteCount, extraFileDataError := filePart.Read(extra[:]); extraByteCount != 0 || extraFileDataError != io.EOF {
			http.Error(responseWriter, "Uploaded size mismatch", 400)
			return
		}
		if hex.EncodeToString(hasher.Sum(nil)) != metadata.FileHash {
			http.Error(responseWriter, "Uploaded file hash mismatch", 409)
			return
		}
		if _, extraMultipartPartError := multipartReader.NextPart(); extraMultipartPartError != io.EOF {
			http.Error(responseWriter, "Unexpected multipart content", 400)
			return
		}
		if apply == nil {
			http.Error(responseWriter, "File application unavailable", 503)
			return
		}
		if verifiedFileApplyError := apply(metadata, transferRoot); verifiedFileApplyError != nil {
			var keepLocalError *KeepLocalError
			var syncConflictError *SyncConflictError
			if errors.As(verifiedFileApplyError, &keepLocalError) {
				writeTransferJSON(responseWriter, TransferCompletion{KeepLocal: true})
				return
			}
			if errors.As(verifiedFileApplyError, &syncConflictError) {
				writeTransferJSON(responseWriter, TransferCompletion{ConflictFile: syncConflictError.ConflictFile})
				return
			}
			http.Error(responseWriter, fmt.Sprintf("Apply verified file: %v", verifiedFileApplyError), 500)
			return
		}
		if verifiedChunkCachingError := cacheVerifiedTransfer(root, transferRoot, metadata); verifiedChunkCachingError != nil {
			http.Error(responseWriter, fmt.Sprintf("Cache verified chunks: %v", verifiedChunkCachingError), 500)
			return
		}
		writeTransferJSON(responseWriter, TransferCompletion{Completed: true, FileHash: metadata.FileHash})
	})
}

// Rabindra Neupane
// NewWholeFileHandler keeps older callers working.
// Whole-file transfer is used only for benchmark comparison.
func NewWholeFileHandler(root string, apply func(FileMetadata, string) error) http.Handler {
	return NewWholeFileSendHandler(root, apply)
}
