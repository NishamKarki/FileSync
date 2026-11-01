// Author: Rabindra Neupane
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
// incomingTransfer tracks one isolated receiver-side transfer until it expires
// or finishes. Conflict and keep-local results are saved for safe retries.
type incomingTransfer struct {
	metadata     FileMetadata
	root         string
	completed    bool
	conflictFile string
	keepLocal    bool
	expires      time.Time
}

// Rabindra Neupane
// transferReceiver stores active transfers and uses one lock to prevent chunk
// writes from racing with final verification and reconstruction.
type transferReceiver struct {
	mutex       sync.Mutex
	projectRoot string
	reconstruct func(FileMetadata, string) error
	transfers   map[string]*incomingTransfer
}

// Rabindra Neupane
// newTransferReceiver creates the shared state used by all transfer endpoints.
func newTransferReceiver(root string, reconstruct func(FileMetadata, string) error) *transferReceiver {
	// Create one transfer map shared by the start, chunk, and complete routes.
	return &transferReceiver{projectRoot: root, reconstruct: reconstruct, transfers: make(map[string]*incomingTransfer)}
}

// Rabindra Neupane
// NewTransferHandler shares the same receiver state across all transfer endpoints.
// An explicit root also allows end-to-end tests to use temporary storage.
func NewTransferHandler(root string, reconstruct func(FileMetadata, string) error) http.Handler {
	// Create one receiver so all three endpoints see the same transfer state.
	receiver := newTransferReceiver(root, reconstruct)
	mux := http.NewServeMux()
	mux.HandleFunc("/transfer/start", receiver.start)
	mux.HandleFunc("/transfer/chunk", receiver.chunk)
	mux.HandleFunc("/transfer/complete", receiver.complete)
	return mux
}

// Rabindra Neupane
// start validates the proposed file, creates an isolated staging folder, and
// returns only the chunk indexes that cannot be reused from verified storage.
func (receiver *transferReceiver) start(responseWriter http.ResponseWriter, request *http.Request) {
	// Validate that transfer creation uses POST.
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", "POST")
		http.Error(responseWriter, "Only POST requests are allowed", 405)
		return
	}

	// Read one strict metadata object from the request.
	var metadata FileMetadata
	if metadataReadingError := readTransferJSON(responseWriter, request, &metadata); metadataReadingError != nil {
		http.Error(responseWriter, "Invalid transfer metadata", 400)
		return
	}
	// Validate file identity, sizes, versions, and hashes before creating files.
	if metadataValidationError := validateFileMetadata(metadata); metadataValidationError != nil {
		http.Error(responseWriter, metadataValidationError.Error(), 400)
		return
	}
	// Check the chunk size before any receiver allocation.
	if metadata.ChunkSize > maxTransferChunkSize {
		http.Error(responseWriter, "Chunk size exceeds 16 MiB transfer limit", 400)
		return
	}

	// Lock active transfer state while cleaning and creating an entry.
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	// Remove expired IDs so abandoned transfers do not fill the active limit.
	// Keep their staging folders available for manual inspection.
	for transferID, transfer := range receiver.transfers {
		if time.Now().After(transfer.expires) {
			delete(receiver.transfers, transferID)
		}
	}
	// Check the active-transfer limit before creating more state.
	if len(receiver.transfers) >= 64 {
		http.Error(responseWriter, "Too many active transfers", 503)
		return
	}
	// Create a random transfer ID that cannot be guessed from a file name.
	var token [16]byte
	if _, transferIDGenerationError := rand.Read(token[:]); transferIDGenerationError != nil {
		http.Error(responseWriter, "Cannot create transfer ID", 500)
		return
	}
	// Create the shared parent used for isolated incoming transfers.
	transferID := hex.EncodeToString(token[:])
	staging := filepath.Join(receiver.projectRoot, "File Chunks", ".transfers")
	// Create the parent folder when this is the first transfer.
	if stagingDirectoryCreationError := os.MkdirAll(staging, 0755); stagingDirectoryCreationError != nil {
		http.Error(responseWriter, "Cannot create transfer storage", 500)
		return
	}
	// Create a unique staging folder with the file name first for readability.
	// Replace stars so they cannot change the temporary-folder pattern.
	stagingName := strings.ReplaceAll(metadata.FileName, "*", "_")
	root, transferDirectoryCreationError := os.MkdirTemp(staging, stagingName+"-incoming-")
	// Check that the isolated transfer folder was created.
	if transferDirectoryCreationError != nil {
		http.Error(responseWriter, "Cannot create transfer folder", 500)
		return
	}
	// Create the file-specific chunk folder inside this transfer.
	folder := filepath.Join(root, metadata.FileName)
	// Check that the chunk folder is ready before staging data.
	if chunkDirectoryCreationError := os.Mkdir(folder, 0755); chunkDirectoryCreationError != nil {
		http.Error(responseWriter, "Cannot create chunk folder", 500)
		return
	}
	// Create the list of chunks that the sender must upload.
	needed := make([]int, 0)
	// Verify each cached chunk before deciding whether it can be reused.
	for index := range metadata.ChunkHashes {
		name := fmt.Sprintf("chunk_%d.chunk", index)
		// Read cached bytes only when their size and hash match this file version.
		chunkData, storedChunkReadingError := readVerifiedChunk(filepath.Join(receiver.projectRoot, "File Chunks", metadata.FileName, name), metadata, index)
		// Request a missing or changed cached chunk from the sender.
		if os.IsNotExist(storedChunkReadingError) || storedChunkReadingError == errChunkMismatch {
			needed = append(needed, index)
			continue
		}
		// Stop when cached chunk storage fails for another reason.
		if storedChunkReadingError != nil {
			http.Error(responseWriter, "Cannot read cached chunk", 500)
			return
		}
		// Copy a verified cached chunk into this isolated transfer folder.
		if stagedChunkWritingError := os.WriteFile(filepath.Join(folder, name), chunkData, 0644); stagedChunkWritingError != nil {
			http.Error(responseWriter, "Cannot stage cached chunk", 500)
			return
		}
	}
	// Save the active transfer and respond with only the required indexes.
	receiver.transfers[transferID] = &incomingTransfer{metadata: metadata, root: root, expires: time.Now().Add(time.Hour)}
	writeTransferJSON(responseWriter, TransferPlan{TransferID: transferID, NeededChunks: needed})
}

// Rabindra Neupane
// errChunkMismatch identifies cached data that cannot be reused safely.
var errChunkMismatch = fmt.Errorf("chunk size or hash mismatch")

// Rabindra Neupane
// readVerifiedChunk reads one bounded chunk and verifies its expected size and hash.
func readVerifiedChunk(path string, metadata FileMetadata, index int) ([]byte, error) {
	// Open the chunk selected for possible reuse.
	file, chunkFileOpeningError := os.Open(path)
	if chunkFileOpeningError != nil {
		return nil, chunkFileOpeningError
	}
	// Close the chunk after verification.
	defer file.Close()
	expected := metadata.ChunkSize
	// Calculate the smaller expected size for the final chunk.
	if index == len(metadata.ChunkHashes)-1 {
		expected = metadata.FileSize - int64(index)*metadata.ChunkSize
	}
	// Check the expected size before allocating memory.
	if expected > maxTransferChunkSize {
		return nil, fmt.Errorf("chunk size exceeds transfer limit")
	}
	// Read one extra byte so oversized cached data is detected.
	chunkData, chunkFileReadingError := io.ReadAll(io.LimitReader(file, expected+1))
	if chunkFileReadingError != nil {
		return nil, chunkFileReadingError
	}
	// Verify both length and SHA-256 before returning reusable bytes.
	hash := sha256.Sum256(chunkData)
	if int64(len(chunkData)) != expected || hex.EncodeToString(hash[:]) != metadata.ChunkHashes[index] {
		return nil, errChunkMismatch
	}
	return chunkData, nil
}

// Rabindra Neupane
// chunk verifies one uploaded chunk before writing it into the transfer's
// private staging folder. It never writes directly into accepted chunk storage.
func (receiver *transferReceiver) chunk(responseWriter http.ResponseWriter, request *http.Request) {
	// Validate that chunk uploads use POST.
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", "POST")
		http.Error(responseWriter, "Only POST requests are allowed", 405)
		return
	}
	// Lock the transfer so uploads cannot race with completion.
	receiver.mutex.Lock()
	// Release the lock after validation and writing finish.
	defer receiver.mutex.Unlock()
	// Read the active transfer selected by the query string.
	transfer := receiver.transfers[request.URL.Query().Get("transferId")]
	// Check that the transfer ID is known and still active.
	if transfer == nil || time.Now().After(transfer.expires) {
		http.Error(responseWriter, "Unknown or expired transfer", 404)
		return
	}
	// Reject writes after a final result has already been saved.
	if transfer.completed {
		http.Error(responseWriter, "Transfer already completed", 409)
		return
	}
	// Read and validate the zero-based chunk index.
	index, chunkIndexParsingError := strconv.Atoi(request.URL.Query().Get("chunkIndex"))
	if chunkIndexParsingError != nil || index < 0 || index >= len(transfer.metadata.ChunkHashes) {
		http.Error(responseWriter, "Invalid chunk index", 400)
		return
	}
	// Calculate the expected size, including a shorter final chunk.
	expected := transfer.metadata.ChunkSize
	if index == len(transfer.metadata.ChunkHashes)-1 {
		expected = transfer.metadata.FileSize - int64(index)*transfer.metadata.ChunkSize
	}
	// Check the size before reading the request body.
	if expected > maxTransferChunkSize {
		http.Error(responseWriter, "Chunk exceeds transfer limit", 400)
		return
	}
	// Limit the body to one byte beyond the expected size to detect overflow.
	request.Body = http.MaxBytesReader(responseWriter, request.Body, expected+1)
	// Read the bounded chunk bytes.
	chunkData, chunkBodyReadingError := io.ReadAll(request.Body)
	if chunkBodyReadingError != nil {
		http.Error(responseWriter, "Cannot read chunk", 400)
		return
	}
	// Create the SHA-256 value for the uploaded data.
	hash := sha256.Sum256(chunkData)
	// Verify size and hash before saving anything.
	if int64(len(chunkData)) != expected || hex.EncodeToString(hash[:]) != transfer.metadata.ChunkHashes[index] {
		http.Error(responseWriter, "Chunk size or hash mismatch", 400)
		return
	}
	// Save verified bytes only inside this transfer's staging folder.
	path := filepath.Join(transfer.root, transfer.metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index))
	if chunkWritingError := os.WriteFile(path, chunkData, 0644); chunkWritingError != nil {
		http.Error(responseWriter, "Cannot save chunk", 500)
		return
	}
	// Respond only after the verified chunk is safely staged.
	responseWriter.WriteHeader(http.StatusOK)
}

// Rabindra Neupane
// complete verifies every staged chunk, reconstructs and applies the file, then
// caches chunks only when the application accepts the completed transfer.
func (receiver *transferReceiver) complete(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	// Validate that completion uses POST.
	if httpRequest.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", "POST")
		http.Error(responseWriter, "Only POST requests are allowed", 405)
		return
	}
	// Read the transfer ID from one strict JSON object.
	var completionRequest struct {
		TransferID string `json:"transferId"`
	}
	// Decode the transfer ID and reject malformed completion data.
	if completionRequestReadingError := readTransferJSON(responseWriter, httpRequest, &completionRequest); completionRequestReadingError != nil {
		http.Error(responseWriter, "Invalid completion request", 400)
		return
	}
	// Lock the transfer so no chunk can change during final verification.
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	// Read the transfer selected by the completion request.
	transfer := receiver.transfers[completionRequest.TransferID]
	// Check that the transfer is known and still active.
	if transfer == nil || time.Now().After(transfer.expires) {
		http.Error(responseWriter, "Unknown or expired transfer", 404)
		return
	}
	// Return the saved result when a completion request is retried.
	if transfer.completed {
		writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, Completed: transfer.conflictFile == "" && !transfer.keepLocal, ConflictFile: transfer.conflictFile, KeepLocal: transfer.keepLocal, FileHash: transfer.metadata.FileHash})
		return
	}
	// Verify every staged chunk again before reconstruction.
	for index := range transfer.metadata.ChunkHashes {
		chunkPath := filepath.Join(transfer.root, transfer.metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index))
		if _, chunkVerificationError := readVerifiedChunk(chunkPath, transfer.metadata, index); chunkVerificationError != nil {
			reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, fmt.Sprintf("Verify chunk %d", index), chunkVerificationError, 409)
			return
		}
	}
	// Reconstruction implementation: Nisham Karki.
	// Check that the application supplied its reconstruction callback.
	if receiver.reconstruct == nil {
		http.Error(responseWriter, "Reconstruction unavailable", 500)
		return
	}
	// Reconstruct and apply the fully verified incoming version.
	if reconstructionError := receiver.reconstruct(transfer.metadata, transfer.root); reconstructionError != nil {
		// Check whether the receiver intentionally kept a newer local version.
		var keepLocalError *KeepLocalError
		if errors.As(reconstructionError, &keepLocalError) {
			transfer.completed, transfer.keepLocal = true, true
			writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, KeepLocal: true})
			return
		}
		// Check whether both devices changed the file and a copy was preserved.
		var conflictError *SyncConflictError
		if errors.As(reconstructionError, &conflictError) {
			transfer.completed = true
			transfer.conflictFile = conflictError.ConflictFile
			// Keep conflict staging for inspection without replacing accepted cache.
			writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, ConflictFile: conflictError.ConflictFile})
			return
		}
		// Respond with the failing application stage for all other errors.
		reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, "Apply verified file", reconstructionError, 500)
		return
	}
	// Save verified chunks for reuse only after the file was accepted.
	if verifiedChunkCachingError := cacheVerifiedTransfer(receiver.projectRoot, transfer.root, transfer.metadata); verifiedChunkCachingError != nil {
		reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, "Cache verified chunks", verifiedChunkCachingError, 500)
		return
	}
	// Save the successful result so a lost acknowledgement can be retried safely.
	transfer.completed = true
	// Respond with the verified complete-file hash.
	writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, Completed: true, FileHash: transfer.metadata.FileHash})
}
