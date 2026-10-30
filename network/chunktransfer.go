package network

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rabindra Neupane
type incomingTransfer struct {
	metadata     FileMetadata
	root         string
	completed    bool
	conflictFile string
	keepLocal    bool
	expires      time.Time
}

// Rabindra Neupane
// The mutex serializes transfer writes and completion in this prototype.
type transferReceiver struct {
	mutex       sync.Mutex
	projectRoot string
	reconstruct func(FileMetadata, string) error
	transfers   map[string]*incomingTransfer
}

// Rabindra Neupane
func newTransferReceiver(root string, reconstruct func(FileMetadata, string) error) *transferReceiver {
	return &transferReceiver{projectRoot: root, reconstruct: reconstruct, transfers: make(map[string]*incomingTransfer)}
}

// Rabindra Neupane
// NewTransferHandler shares the same receiver state across all transfer endpoints.
// An explicit root also allows end-to-end tests to use temporary storage.
func NewTransferHandler(root string, reconstruct func(FileMetadata, string) error) http.Handler {
	receiver := newTransferReceiver(root, reconstruct)
	mux := http.NewServeMux()
	mux.HandleFunc("/transfer/start", receiver.start)
	mux.HandleFunc("/transfer/chunk", receiver.chunk)
	mux.HandleFunc("/transfer/complete", receiver.complete)
	return mux
}

// Rabindra Neupane
func (receiver *transferReceiver) start(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", "POST")
		http.Error(responseWriter, "Only POST requests are allowed", 405)
		return
	}
	var metadata FileMetadata
	if metadataReadingError := readTransferJSON(responseWriter, request, &metadata); metadataReadingError != nil {
		http.Error(responseWriter, "Invalid transfer metadata", 400)
		return
	}
	if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
		http.Error(responseWriter, metadataValidationError.Error(), 400)
		return
	}
	if metadata.ChunkSize > maxTransferChunkSize {
		http.Error(responseWriter, "Chunk size exceeds 16 MiB transfer limit", 400)
		return
	}
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	// Expired transfers no longer accept writes. Their staging folders are kept
	// for inspection; restarting the app also invalidates in-memory transfer IDs.
	for transferID, transfer := range receiver.transfers {
		if time.Now().After(transfer.expires) {
			delete(receiver.transfers, transferID)
		}
	}
	if len(receiver.transfers) >= 64 {
		http.Error(responseWriter, "Too many active transfers", 503)
		return
	}
	var token [16]byte
	if _, transferIDGenerationError := rand.Read(token[:]); transferIDGenerationError != nil {
		http.Error(responseWriter, "Cannot create transfer ID", 500)
		return
	}
	transferID := hex.EncodeToString(token[:])
	staging := filepath.Join(receiver.projectRoot, "File Chunks", ".transfers")
	if stagingDirectoryCreationError := os.MkdirAll(staging, 0755); stagingDirectoryCreationError != nil {
		http.Error(responseWriter, "Cannot create transfer storage", 500)
		return
	}
	// Put the file name first while preserving unique staging per transfer.
	// Escape stars so they cannot become MkdirTemp pattern placeholders.
	stagingName := strings.ReplaceAll(metadata.FileName, "*", "_")
	root, transferDirectoryCreationError := os.MkdirTemp(staging, stagingName+"-incoming-")
	if transferDirectoryCreationError != nil {
		http.Error(responseWriter, "Cannot create transfer folder", 500)
		return
	}
	folder := filepath.Join(root, metadata.FileName)
	if chunkDirectoryCreationError := os.Mkdir(folder, 0755); chunkDirectoryCreationError != nil {
		http.Error(responseWriter, "Cannot create chunk folder", 500)
		return
	}
	needed := make([]int, 0)
	for index := range metadata.ChunkHashes {
		name := fmt.Sprintf("chunk_%d.chunk", index)
		// Copy verified cached bytes into an isolated snapshot. Never reuse by
		// filename alone, and never modify cached chunks while receiving uploads.
		chunkData, storedChunkReadingError := readVerifiedChunk(filepath.Join(receiver.projectRoot, "File Chunks", metadata.FileName, name), metadata, index)
		if os.IsNotExist(storedChunkReadingError) || storedChunkReadingError == errChunkMismatch {
			needed = append(needed, index)
			continue
		}
		if storedChunkReadingError != nil {
			http.Error(responseWriter, "Cannot read cached chunk", 500)
			return
		}
		if stagedChunkWritingError := os.WriteFile(filepath.Join(folder, name), chunkData, 0644); stagedChunkWritingError != nil {
			http.Error(responseWriter, "Cannot stage cached chunk", 500)
			return
		}
	}
	receiver.transfers[transferID] = &incomingTransfer{metadata: metadata, root: root, expires: time.Now().Add(time.Hour)}
	writeTransferJSON(responseWriter, TransferPlan{TransferID: transferID, NeededChunks: needed})
}

// Rabindra Neupane
var errChunkMismatch = fmt.Errorf("chunk size or hash mismatch")

// Rabindra Neupane
// A maximum chunk size bounds allocations for uploads and cached chunk copies.
// Rabindra Neupane
func readVerifiedChunk(path string, metadata FileMetadata, index int) ([]byte, error) {
	file, chunkFileOpeningError := os.Open(path)
	if chunkFileOpeningError != nil {
		return nil, chunkFileOpeningError
	}
	defer file.Close()
	expected := metadata.ChunkSize
	if index == len(metadata.ChunkHashes)-1 {
		expected = metadata.FileSize - int64(index)*metadata.ChunkSize
	}
	if expected > maxTransferChunkSize {
		return nil, fmt.Errorf("chunk size exceeds transfer limit")
	}
	chunkData, chunkFileReadingError := io.ReadAll(io.LimitReader(file, expected+1))
	if chunkFileReadingError != nil {
		return nil, chunkFileReadingError
	}
	hash := sha256.Sum256(chunkData)
	if int64(len(chunkData)) != expected || hex.EncodeToString(hash[:]) != metadata.ChunkHashes[index] {
		return nil, errChunkMismatch
	}
	return chunkData, nil
}

// Rabindra Neupane
func (receiver *transferReceiver) chunk(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", "POST")
		http.Error(responseWriter, "Only POST requests are allowed", 405)
		return
	}
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	transfer := receiver.transfers[request.URL.Query().Get("transferId")]
	if transfer == nil || time.Now().After(transfer.expires) {
		http.Error(responseWriter, "Unknown or expired transfer", 404)
		return
	}
	if transfer.completed {
		http.Error(responseWriter, "Transfer already completed", 409)
		return
	}
	index, chunkIndexParsingError := strconv.Atoi(request.URL.Query().Get("chunkIndex"))
	if chunkIndexParsingError != nil || index < 0 || index >= len(transfer.metadata.ChunkHashes) {
		http.Error(responseWriter, "Invalid chunk index", 400)
		return
	}
	expected := transfer.metadata.ChunkSize
	if index == len(transfer.metadata.ChunkHashes)-1 {
		expected = transfer.metadata.FileSize - int64(index)*transfer.metadata.ChunkSize
	}
	if expected > maxTransferChunkSize {
		http.Error(responseWriter, "Chunk exceeds transfer limit", 400)
		return
	}
	request.Body = http.MaxBytesReader(responseWriter, request.Body, expected+1)
	chunkData, chunkBodyReadingError := io.ReadAll(request.Body)
	if chunkBodyReadingError != nil {
		http.Error(responseWriter, "Cannot read chunk", 400)
		return
	}
	hash := sha256.Sum256(chunkData)
	if int64(len(chunkData)) != expected || hex.EncodeToString(hash[:]) != transfer.metadata.ChunkHashes[index] {
		http.Error(responseWriter, "Chunk size or hash mismatch", 400)
		return
	}
	path := filepath.Join(transfer.root, transfer.metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index))
	if chunkWritingError := os.WriteFile(path, chunkData, 0644); chunkWritingError != nil {
		http.Error(responseWriter, "Cannot save chunk", 500)
		return
	}
	responseWriter.WriteHeader(http.StatusOK)
}

// Rabindra Neupane
func (receiver *transferReceiver) complete(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	if httpRequest.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", "POST")
		http.Error(responseWriter, "Only POST requests are allowed", 405)
		return
	}
	var completionRequest struct {
		TransferID string `json:"transferId"`
	}
	if completionRequestReadingError := readTransferJSON(responseWriter, httpRequest, &completionRequest); completionRequestReadingError != nil {
		http.Error(responseWriter, "Invalid completion request", 400)
		return
	}
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	transfer := receiver.transfers[completionRequest.TransferID]
	if transfer == nil || time.Now().After(transfer.expires) {
		http.Error(responseWriter, "Unknown or expired transfer", 404)
		return
	}
	if transfer.completed {
		writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, Completed: transfer.conflictFile == "" && !transfer.keepLocal, ConflictFile: transfer.conflictFile, KeepLocal: transfer.keepLocal, FileHash: transfer.metadata.FileHash})
		return
	}
	for index := range transfer.metadata.ChunkHashes {
		chunkPath := filepath.Join(transfer.root, transfer.metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index))
		if _, chunkVerificationError := readVerifiedChunk(chunkPath, transfer.metadata, index); chunkVerificationError != nil {
			reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, fmt.Sprintf("Verify chunk %d", index), chunkVerificationError, 409)
			return
		}
	}
	// Reconstruction implementation: Nisham Karki
	if receiver.reconstruct == nil {
		http.Error(responseWriter, "Reconstruction unavailable", 500)
		return
	}
	if reconstructionError := receiver.reconstruct(transfer.metadata, transfer.root); reconstructionError != nil {
		var keepLocalError *KeepLocalError
		if errors.As(reconstructionError, &keepLocalError) {
			transfer.completed, transfer.keepLocal = true, true
			writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, KeepLocal: true})
			return
		}
		var conflictError *SyncConflictError
		if errors.As(reconstructionError, &conflictError) {
			transfer.completed = true
			transfer.conflictFile = conflictError.ConflictFile
			// Keep staged chunks for inspection, including conflicting transfers.
			// Conflicting chunks must not replace the accepted file's cache.
			writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, ConflictFile: conflictError.ConflictFile})
			return
		}
		reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, "Apply verified file", reconstructionError, 500)
		return
	}
	if verifiedChunkCachingError := cacheVerifiedTransfer(receiver.projectRoot, transfer.root, transfer.metadata); verifiedChunkCachingError != nil {
		reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, "Cache verified chunks", verifiedChunkCachingError, 500)
		return
	}
	transfer.completed = true
	// Preserve staged chunks after completion so each transfer can be inspected.
	writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, Completed: true, FileHash: transfer.metadata.FileHash})
}
