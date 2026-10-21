package network

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

//Rabindra Neupane
type incomingTransfer struct {
	metadata  FileMetadata
	root      string
	completed bool
	//Rabindra Neupane
	conflictFile string
	expires      time.Time
}

//Rabindra Neupane
// The mutex serializes transfer writes and completion in this prototype.
type transferReceiver struct {
	mutex       sync.Mutex
	projectRoot string
	reconstruct func(FileMetadata, string) error
	transfers   map[string]*incomingTransfer
}

//Rabindra Neupane
func newTransferReceiver(root string, reconstruct func(FileMetadata, string) error) *transferReceiver {
	return &transferReceiver{projectRoot: root, reconstruct: reconstruct, transfers: make(map[string]*incomingTransfer)}
}

//Rabindra Neupane
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

//Rabindra Neupane
func readTransferJSON(w http.ResponseWriter, r *http.Request, value interface{}) error {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		return fmt.Errorf("only POST requests are allowed")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}

//Rabindra Neupane
func writeTransferJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}

//Rabindra Neupane
func (receiver *transferReceiver) start(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Only POST requests are allowed", 405)
		return
	}
	var metadata FileMetadata
	if err := readTransferJSON(w, r, &metadata); err != nil {
		http.Error(w, "Invalid transfer metadata", 400)
		return
	}
	if err := validateFileMetadata(metadata); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if metadata.ChunkSize > maxTransferChunkSize {
		http.Error(w, "Chunk size exceeds 16 MiB transfer limit", 400)
		return
	}
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	// Expired transfers no longer accept writes. Their staging folders are kept
	// for inspection; restarting the app also invalidates in-memory transfer IDs.
	for id, transfer := range receiver.transfers {
		if time.Now().After(transfer.expires) {
			delete(receiver.transfers, id)
		}
	}
	if len(receiver.transfers) >= 64 {
		http.Error(w, "Too many active transfers", 503)
		return
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		http.Error(w, "Cannot create transfer ID", 500)
		return
	}
	id := hex.EncodeToString(token[:])
	staging := filepath.Join(receiver.projectRoot, "File Chunks", ".transfers")
	if err := os.MkdirAll(staging, 0755); err != nil {
		http.Error(w, "Cannot create transfer storage", 500)
		return
	}
	root, err := os.MkdirTemp(staging, "incoming-")
	if err != nil {
		http.Error(w, "Cannot create transfer folder", 500)
		return
	}
	folder := filepath.Join(root, metadata.FileName)
	if err := os.Mkdir(folder, 0755); err != nil {
		http.Error(w, "Cannot create chunk folder", 500)
		return
	}
	needed := make([]int, 0)
	for index := range metadata.ChunkHashes {
		name := fmt.Sprintf("chunk_%d.chunk", index)
		// Copy verified cached bytes into an isolated snapshot. Never reuse by
		// filename alone, and never modify cached chunks while receiving uploads.
		data, err := readVerifiedChunk(filepath.Join(receiver.projectRoot, "File Chunks", metadata.FileName, name), metadata, index)
		if os.IsNotExist(err) || err == errChunkMismatch {
			needed = append(needed, index)
			continue
		}
		if err != nil {
			http.Error(w, "Cannot read cached chunk", 500)
			return
		}
		if err := os.WriteFile(filepath.Join(folder, name), data, 0644); err != nil {
			http.Error(w, "Cannot stage cached chunk", 500)
			return
		}
	}
	receiver.transfers[id] = &incomingTransfer{metadata: metadata, root: root, expires: time.Now().Add(time.Hour)}
	writeTransferJSON(w, TransferPlan{TransferID: id, NeededChunks: needed})
}

//Rabindra Neupane
var errChunkMismatch = fmt.Errorf("chunk size or hash mismatch")

//Rabindra Neupane
// A maximum chunk size bounds allocations for uploads and cached chunk copies.
const maxTransferChunkSize = 16 << 20

//Rabindra Neupane
func readVerifiedChunk(path string, metadata FileMetadata, index int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	expected := metadata.ChunkSize
	if index == len(metadata.ChunkHashes)-1 {
		expected = metadata.FileSize - int64(index)*metadata.ChunkSize
	}
	if expected > maxTransferChunkSize {
		return nil, fmt.Errorf("chunk size exceeds transfer limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, expected+1))
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if int64(len(data)) != expected || hex.EncodeToString(hash[:]) != metadata.ChunkHashes[index] {
		return nil, errChunkMismatch
	}
	return data, nil
}

//Rabindra Neupane
func (receiver *transferReceiver) chunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Only POST requests are allowed", 405)
		return
	}
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	transfer := receiver.transfers[r.URL.Query().Get("transferId")]
	if transfer == nil || time.Now().After(transfer.expires) {
		http.Error(w, "Unknown or expired transfer", 404)
		return
	}
	if transfer.completed {
		http.Error(w, "Transfer already completed", 409)
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("chunkIndex"))
	if err != nil || index < 0 || index >= len(transfer.metadata.ChunkHashes) {
		http.Error(w, "Invalid chunk index", 400)
		return
	}
	expected := transfer.metadata.ChunkSize
	if index == len(transfer.metadata.ChunkHashes)-1 {
		expected = transfer.metadata.FileSize - int64(index)*transfer.metadata.ChunkSize
	}
	if expected > maxTransferChunkSize {
		http.Error(w, "Chunk exceeds transfer limit", 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, expected+1)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Cannot read chunk", 400)
		return
	}
	hash := sha256.Sum256(data)
	if int64(len(data)) != expected || hex.EncodeToString(hash[:]) != transfer.metadata.ChunkHashes[index] {
		http.Error(w, "Chunk size or hash mismatch", 400)
		return
	}
	path := filepath.Join(transfer.root, transfer.metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index))
	if err := os.WriteFile(path, data, 0644); err != nil {
		http.Error(w, "Cannot save chunk", 500)
		return
	}
	w.WriteHeader(http.StatusOK)
}

//Rabindra Neupane
func (receiver *transferReceiver) complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Only POST requests are allowed", 405)
		return
	}
	var request struct {
		TransferID string `json:"transferId"`
	}
	if err := readTransferJSON(w, r, &request); err != nil {
		http.Error(w, "Invalid completion request", 400)
		return
	}
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	transfer := receiver.transfers[request.TransferID]
	if transfer == nil || time.Now().After(transfer.expires) {
		http.Error(w, "Unknown or expired transfer", 404)
		return
	}
	if transfer.completed {
		//Rabindra Neupane
		writeTransferJSON(w, TransferCompletion{TransferID: request.TransferID, Completed: transfer.conflictFile == "", ConflictFile: transfer.conflictFile})
		return
	}
	for index := range transfer.metadata.ChunkHashes {
		path := filepath.Join(transfer.root, transfer.metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index))
		if _, err := readVerifiedChunk(path, transfer.metadata, index); err != nil {
			//Rabindra Neupane
			reportTransferFailure(w, request.TransferID, transfer.metadata.FileName, fmt.Sprintf("Verify chunk %d", index), err, 409)
			return
		}
	}
	// Reconstruction implementation: Nisham Karki
	// Network integration: Rabindra Neupane
	if receiver.reconstruct == nil {
		http.Error(w, "Reconstruction unavailable", 500)
		return
	}
	if err := receiver.reconstruct(transfer.metadata, transfer.root); err != nil {
		//Rabindra Neupane
		var conflict *SyncConflictError
		if errors.As(err, &conflict) {
			transfer.completed = true
			transfer.conflictFile = conflict.ConflictFile
			// Conflicting chunks must not replace the accepted file's cache.
			writeTransferJSON(w, TransferCompletion{TransferID: request.TransferID, ConflictFile: conflict.ConflictFile})
			return
		}
		//Rabindra Neupane
		reportTransferFailure(w, request.TransferID, transfer.metadata.FileName, "Apply verified file", err, 500)
		return
	}
	// Cache verified chunks for the next comparison. Stale trailing chunks are
	// ignored by metadata comparison; reconstruction uses the isolated folder.
	folder := filepath.Join(receiver.projectRoot, "File Chunks", transfer.metadata.FileName)
	if err := os.MkdirAll(folder, 0755); err != nil {
		//Rabindra Neupane
		reportTransferFailure(w, request.TransferID, transfer.metadata.FileName, "Create chunk cache folder", err, 500)
		return
	}
	for index := range transfer.metadata.ChunkHashes {
		name := fmt.Sprintf("chunk_%d.chunk", index)
		data, err := readVerifiedChunk(filepath.Join(transfer.root, transfer.metadata.FileName, name), transfer.metadata, index)
		if err != nil {
			//Rabindra Neupane
			reportTransferFailure(w, request.TransferID, transfer.metadata.FileName, fmt.Sprintf("Read verified chunk %d for cache", index), err, 500)
			return
		}
		if err := os.WriteFile(filepath.Join(folder, name), data, 0644); err != nil {
			//Rabindra Neupane
			reportTransferFailure(w, request.TransferID, transfer.metadata.FileName, fmt.Sprintf("Cache verified chunk %d", index), err, 500)
			return
		}
	}
	transfer.completed = true
	//Rabindra Neupane
	writeTransferJSON(w, TransferCompletion{TransferID: request.TransferID, Completed: true})
}

//Rabindra Neupane
// Log the exact failing stage on B and return its explanation to A.
func reportTransferFailure(w http.ResponseWriter, transferID, fileName, stage string, err error, status int) {
	fmt.Printf("Transfer completion failed: id=%q file=%q stage=%q error=%q\n", transferID, fileName, stage, err.Error())
	http.Error(w, fmt.Sprintf("%s: %v", stage, err), status)
}
