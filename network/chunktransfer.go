// Author: Rabindra Neupane
package network

import (
	"context"
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

const (
	maxConcurrentTransfersPerPeer  = 4
	defaultTransferStagingTTL      = time.Hour
	defaultTransferCleanupInterval = time.Minute
)

// Rabindra Neupane
// incomingTransfer tracks one isolated receiver-side transfer until it expires
// or finishes. Conflict and keep-local results are saved for safe retries.
type incomingTransfer struct {
	mu           sync.RWMutex
	senderKey    string
	metadata     FileMetadata
	root         string
	reusedChunks []bool
	completed    bool
	conflictFile string
	keepLocal    bool
	expires      time.Time
}

// Rabindra Neupane
// transferReceiver stores active transfers and uses one lock to prevent chunk
// writes from racing with final verification and reconstruction.
type transferReceiver struct {
	mutex            sync.Mutex
	projectRoot      string
	reconstruct      func(FileMetadata, string) error
	transferAccepted func()
	registry         *DeviceRegistry
	chunkIndex       *ChunkFileIndex
	transfers        map[string]*incomingTransfer
	stagingTTL       time.Duration
	cleanupInterval  time.Duration
}

// Rabindra Neupane
// newTransferReceiver creates the shared state used by all transfer endpoints.
func newTransferReceiver(root string, reconstruct func(FileMetadata, string) error, transferAccepted func(), registry *DeviceRegistry, chunkIndex *ChunkFileIndex) *transferReceiver {
	return &transferReceiver{
		projectRoot:      root,
		reconstruct:      reconstruct,
		transferAccepted: transferAccepted,
		registry:         registry,
		chunkIndex:       chunkIndex,
		transfers:        make(map[string]*incomingTransfer),
		stagingTTL:       defaultTransferStagingTTL,
		cleanupInterval:  defaultTransferCleanupInterval,
	}
}

// NewTransferHandlerWithContext runs the mTLS transfer handler and a background
// cleanup ticker tied to ctx to purge expired staging folders.
func NewTransferHandlerWithContext(ctx context.Context, root string, reconstruct func(FileMetadata, string) error, transferAccepted func(), registry *DeviceRegistry, chunkIndex *ChunkFileIndex) http.Handler {
	receiver := newTransferReceiver(root, reconstruct, transferAccepted, registry, chunkIndex)
	if ctx != nil {
		go receiver.cleanupLoop(ctx)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/transfer/start", receiver.start)
	mux.HandleFunc("/transfer/chunk", receiver.chunk)
	mux.HandleFunc("/transfer/complete", receiver.complete)
	return mux
}

func (receiver *transferReceiver) cleanupLoop(ctx context.Context) {
	interval := receiver.cleanupInterval
	if interval <= 0 {
		interval = defaultTransferCleanupInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			receiver.cleanupExpiredAndStaleStaging()
		}
	}
}

func (receiver *transferReceiver) cleanupExpiredAndStaleStaging() {
	now := time.Now()
	ttl := receiver.stagingTTL
	if ttl <= 0 {
		ttl = defaultTransferStagingTTL
	}

	receiver.mutex.Lock()
	activeRoots := make(map[string]bool)
	for transferID, transfer := range receiver.transfers {
		if now.After(transfer.expires) {
			os.RemoveAll(transfer.root)
			delete(receiver.transfers, transferID)
			continue
		}
		if transfer.root != "" {
			activeRoots[filepath.Clean(transfer.root)] = true
		}
	}
	receiver.mutex.Unlock()

	stagingParent := filepath.Join(receiver.projectRoot, "File Chunks", ".transfers")
	entries, err := os.ReadDir(stagingParent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Clean(filepath.Join(stagingParent, entry.Name()))
		if activeRoots[candidate] {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && now.Sub(info.ModTime()) > ttl {
			_ = os.RemoveAll(candidate)
		}
	}
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
	certDeviceID, authErr := authenticatePeerMTLS(receiver.registry, request, "")
	if authErr != nil {
		http.Error(responseWriter, authErr.Error(), http.StatusUnauthorized)
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
	if metadata.DeviceID != certDeviceID {
		http.Error(responseWriter, "Sender device identity does not match authenticated mTLS certificate", http.StatusUnauthorized)
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
	// Remove expired IDs and their staging folders so abandoned transfers do not leak disk space.
	now := time.Now()
	activeForSender := 0
	for transferID, transfer := range receiver.transfers {
		if now.After(transfer.expires) {
			os.RemoveAll(transfer.root)
			delete(receiver.transfers, transferID)
			continue
		}
		if transfer.senderKey == certDeviceID && !transfer.completed {
			activeForSender++
		}
	}
	// Enforce per-peer concurrency cap so one peer cannot starve other devices.
	if activeForSender >= maxConcurrentTransfersPerPeer {
		http.Error(responseWriter, "Too many concurrent transfers for this peer", http.StatusTooManyRequests)
		return
	}
	// Check the global active-transfer limit before creating more state.
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
	// Collect prior staging directories for this file so interrupted transfers can resume verified chunks.
	previousStagingFolders := findPriorStagingFolders(staging, stagingName+"-incoming-", root)

	// Create the list of chunks that the sender must upload.
	needed := make([]int, 0)
	reusedChunks := make([]bool, len(metadata.ChunkHashes))
	// Verify each cached chunk before deciding whether it can be reused.
	for index := range metadata.ChunkHashes {
		name := fmt.Sprintf("chunk_%d.chunk", index)
		// Read cached bytes only when their size and hash match this file version.
		_, storedChunkReadingError := readVerifiedChunk(filepath.Join(receiver.projectRoot, "File Chunks", metadata.FileName, name), metadata, index)
		// If not in accepted cache, attempt to resume from an earlier interrupted staging folder.
		if os.IsNotExist(storedChunkReadingError) || storedChunkReadingError == errChunkMismatch {
			if resumedData, resumed := readResumedStagedChunk(previousStagingFolders, metadata, index); resumed {
				stagedPath := filepath.Join(folder, name)
				if writeErr := os.WriteFile(stagedPath, resumedData, 0644); writeErr == nil {
					continue
				}
			}
			needed = append(needed, index)
			continue
		}
		// Stop when cached chunk storage fails for another reason.
		if storedChunkReadingError != nil {
			http.Error(responseWriter, "Cannot read cached chunk", 500)
			return
		}
		// Remember verified cached data without copying it during transfer startup.
		reusedChunks[index] = true
	}
	// Save the active transfer and respond with only the required indexes.
	ttl := receiver.stagingTTL
	if ttl <= 0 {
		ttl = defaultTransferStagingTTL
	}
	receiver.transfers[transferID] = &incomingTransfer{senderKey: certDeviceID, metadata: metadata, root: root, reusedChunks: reusedChunks, expires: time.Now().Add(ttl)}
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
	certDeviceID, authErr := authenticatePeerMTLS(receiver.registry, request, "")
	if authErr != nil {
		http.Error(responseWriter, authErr.Error(), http.StatusUnauthorized)
		return
	}
	// Look up the active transfer briefly, then allow concurrent chunk uploads under RLock.
	receiver.mutex.Lock()
	transfer := receiver.transfers[request.URL.Query().Get("transferId")]
	receiver.mutex.Unlock()
	if transfer == nil {
		http.Error(responseWriter, "Unknown or expired transfer", 404)
		return
	}
	transfer.mu.RLock()
	defer transfer.mu.RUnlock()
	if transfer.senderKey != certDeviceID {
		http.Error(responseWriter, "Transfer belongs to a different peer", http.StatusUnauthorized)
		return
	}
	if time.Now().After(transfer.expires) {
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
	certDeviceID, authErr := authenticatePeerMTLS(receiver.registry, httpRequest, "")
	if authErr != nil {
		http.Error(responseWriter, authErr.Error(), http.StatusUnauthorized)
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
	// Look up the transfer briefly, then lock it exclusively so no chunk upload races completion.
	receiver.mutex.Lock()
	transfer := receiver.transfers[completionRequest.TransferID]
	receiver.mutex.Unlock()
	if transfer == nil {
		http.Error(responseWriter, "Unknown or expired transfer", 404)
		return
	}
	transfer.mu.Lock()
	defer transfer.mu.Unlock()
	if transfer.senderKey != certDeviceID {
		http.Error(responseWriter, "Transfer belongs to a different peer", http.StatusUnauthorized)
		return
	}
	if time.Now().After(transfer.expires) {
		http.Error(responseWriter, "Unknown or expired transfer", 404)
		return
	}
	// Return the saved result when a completion request is retried.
	if transfer.completed {
		writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, Completed: transfer.conflictFile == "" && !transfer.keepLocal, ConflictFile: transfer.conflictFile, KeepLocal: transfer.keepLocal, FileHash: transfer.metadata.FileHash})
		return
	}
	// Materialize only the cached chunks selected during transfer planning.
	for index, reusedChunk := range transfer.reusedChunks {
		if !reusedChunk {
			continue
		}
		chunkName := fmt.Sprintf("chunk_%d.chunk", index)
		cachedChunkPath := filepath.Join(receiver.projectRoot, "File Chunks", transfer.metadata.FileName, chunkName)
		chunkData, cachedChunkReadingError := readVerifiedChunk(cachedChunkPath, transfer.metadata, index)
		if cachedChunkReadingError != nil {
			reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, fmt.Sprintf("Read cached chunk %d", index), cachedChunkReadingError, 409)
			return
		}
		stagedChunkPath := filepath.Join(transfer.root, transfer.metadata.FileName, chunkName)
		if stagedChunkWritingError := os.WriteFile(stagedChunkPath, chunkData, 0644); stagedChunkWritingError != nil {
			reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, fmt.Sprintf("Stage cached chunk %d", index), stagedChunkWritingError, 500)
			return
		}
	}
	// Verify every staged chunk again before reconstruction, falling back to peer retrieval if needed.
	for index := range transfer.metadata.ChunkHashes {
		chunkPath := filepath.Join(transfer.root, transfer.metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index))
		if _, chunkVerificationError := readVerifiedChunk(chunkPath, transfer.metadata, index); chunkVerificationError != nil {
			if receiver.registry != nil && receiver.registry.Status().Registered {
				if peerData, _, peerErr := receiver.registry.RetrieveChunk(httpRequest.Context(), transfer.metadata.ChunkHashes[index]); peerErr == nil {
					if writeErr := os.WriteFile(chunkPath, peerData, 0644); writeErr == nil {
						if _, recheckErr := readVerifiedChunk(chunkPath, transfer.metadata, index); recheckErr == nil {
							continue
						}
					}
				}
			}
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
			os.RemoveAll(transfer.root)
			writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, KeepLocal: true})
			return
		}
		// Check whether both devices changed the file and a copy was preserved.
		var conflictError *SyncConflictError
		if errors.As(reconstructionError, &conflictError) {
			transfer.completed = true
			transfer.conflictFile = conflictError.ConflictFile
			os.RemoveAll(transfer.root)
			writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, ConflictFile: conflictError.ConflictFile})
			return
		}
		// Respond with the failing application stage for all other errors.
		reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, "Apply verified file", reconstructionError, 500)
		return
	}
	// Save verified chunks for reuse only after the file was accepted and index them.
	if verifiedChunkCachingError := cacheVerifiedTransfer(receiver.projectRoot, transfer.root, transfer.metadata, receiver.chunkIndex); verifiedChunkCachingError != nil {
		reportTransferFailure(responseWriter, completionRequest.TransferID, transfer.metadata.FileName, "Cache verified chunks", verifiedChunkCachingError, 500)
		return
	}
	// Clean up completed staging folder and any prior interrupted staging folders for this file.
	cleanupFileStagingFolders(filepath.Join(receiver.projectRoot, "File Chunks", ".transfers"), strings.ReplaceAll(transfer.metadata.FileName, "*", "_")+"-incoming-")
	// Report the accepted cache after every chunk is safely available to peers.
	if receiver.transferAccepted != nil {
		go receiver.transferAccepted()
	}
	// Save the successful result so a lost acknowledgement can be retried safely.
	transfer.completed = true
	// Respond with the verified complete-file hash.
	writeTransferJSON(responseWriter, TransferCompletion{TransferID: completionRequest.TransferID, Completed: true, FileHash: transfer.metadata.FileHash})
}

func findPriorStagingFolders(stagingRoot, prefix, currentRoot string) []string {
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return nil
	}
	var folders []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		candidate := filepath.Join(stagingRoot, entry.Name())
		if filepath.Clean(candidate) != filepath.Clean(currentRoot) {
			folders = append(folders, candidate)
		}
	}
	return folders
}

func readResumedStagedChunk(folders []string, metadata FileMetadata, index int) ([]byte, bool) {
	chunkName := fmt.Sprintf("chunk_%d.chunk", index)
	for _, folder := range folders {
		chunkPath := filepath.Join(folder, metadata.FileName, chunkName)
		if data, err := readVerifiedChunk(chunkPath, metadata, index); err == nil {
			return data, true
		}
	}
	return nil, false
}

func cleanupFileStagingFolders(stagingRoot, prefix string) {
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			_ = os.RemoveAll(filepath.Join(stagingRoot, entry.Name()))
		}
	}
}
