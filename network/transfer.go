package network

import (
	"bytes"
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
	"strings"
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
	//Rabindra Neupane
	keepLocal bool
	expires   time.Time
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
	//Rabindra Neupane
	// Put the file name first while preserving unique staging per transfer.
	// Escape stars so they cannot become MkdirTemp pattern placeholders.
	stagingName := strings.ReplaceAll(metadata.FileName, "*", "_")
	root, err := os.MkdirTemp(staging, stagingName+"-incoming-")
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
		writeTransferJSON(w, TransferCompletion{TransferID: request.TransferID, Completed: transfer.conflictFile == "" && !transfer.keepLocal, ConflictFile: transfer.conflictFile, KeepLocal: transfer.keepLocal, FileHash: transfer.metadata.FileHash})
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
		var keep *KeepLocalError
		if errors.As(err, &keep) {
			transfer.completed, transfer.keepLocal = true, true
			writeTransferJSON(w, TransferCompletion{TransferID: request.TransferID, KeepLocal: true})
			return
		}
		//Rabindra Neupane
		var conflict *SyncConflictError
		if errors.As(err, &conflict) {
			transfer.completed = true
			transfer.conflictFile = conflict.ConflictFile
			//Rabindra Neupane
			// Keep staged chunks for inspection, including conflicting transfers.
			// Conflicting chunks must not replace the accepted file's cache.
			writeTransferJSON(w, TransferCompletion{TransferID: request.TransferID, ConflictFile: conflict.ConflictFile})
			return
		}
		//Rabindra Neupane
		reportTransferFailure(w, request.TransferID, transfer.metadata.FileName, "Apply verified file", err, 500)
		return
	}
	//Rabindra Neupane
	if err := cacheVerifiedTransfer(receiver.projectRoot, transfer.root, transfer.metadata); err != nil {
		reportTransferFailure(w, request.TransferID, transfer.metadata.FileName, "Cache verified chunks", err, 500)
		return
	}
	transfer.completed = true
	//Rabindra Neupane
	// Preserve staged chunks after completion so each transfer can be inspected.
	//Rabindra Neupane
	writeTransferJSON(w, TransferCompletion{TransferID: request.TransferID, Completed: true, FileHash: transfer.metadata.FileHash})
}

//Rabindra Neupane
// Log the exact failing stage on B and return its explanation to A.
func reportTransferFailure(w http.ResponseWriter, transferID, fileName, stage string, err error, status int) {
	fmt.Printf("Transfer completion failed: id=%q file=%q stage=%q error=%q\n", transferID, fileName, stage, err.Error())
	http.Error(w, fmt.Sprintf("%s: %v", stage, err), status)
}

//Rabindra Neupane
// Whole-file uploads cannot bypass Santosh's conflict decision or hash verification.
func NewWholeFileHandler(root string, apply func(FileMetadata, string) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "Only POST requests are allowed", http.StatusMethodNotAllowed)
			return
		}
		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "Expected multipart metadata and file", 400)
			return
		}
		part, err := reader.NextPart()
		if err != nil || part.FormName() != "metadata" {
			http.Error(w, "Transfer metadata is required; update both peers", 400)
			return
		}
		data, err := io.ReadAll(io.LimitReader(part, (8<<20)+1))
		if err != nil || len(data) > 8<<20 {
			http.Error(w, "Invalid transfer metadata", 400)
			return
		}
		var metadata FileMetadata
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&metadata); err != nil {
			http.Error(w, "Invalid transfer metadata", 400)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "Expected one metadata object", 400)
			return
		}
		if err := validateFileMetadata(metadata); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if metadata.ChunkSize > maxTransferChunkSize {
			http.Error(w, "Chunk size exceeds transfer limit", 400)
			return
		}
		file, err := reader.NextPart()
		if err != nil || file.FormName() != "file" || file.FileName() != metadata.FileName {
			http.Error(w, "Expected matching file part", 400)
			return
		}
		staging := filepath.Join(root, "File Chunks", ".transfers")
		if err := os.MkdirAll(staging, 0755); err != nil {
			http.Error(w, "Cannot create transfer storage", 500)
			return
		}
		transferRoot, err := os.MkdirTemp(staging, strings.ReplaceAll(metadata.FileName, "*", "_")+"-incoming-")
		if err != nil {
			http.Error(w, "Cannot create transfer folder", 500)
			return
		}
		folder := filepath.Join(transferRoot, metadata.FileName)
		if err := os.Mkdir(folder, 0755); err != nil {
			http.Error(w, "Cannot create chunk folder", 500)
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
			if _, err := io.ReadFull(file, chunk); err != nil {
				http.Error(w, "Incomplete uploaded file", 400)
				return
			}
			sum := sha256.Sum256(chunk)
			if hex.EncodeToString(sum[:]) != expected {
				http.Error(w, "Uploaded chunk hash mismatch", 409)
				return
			}
			if err := os.WriteFile(filepath.Join(folder, fmt.Sprintf("chunk_%d.chunk", index)), chunk, 0644); err != nil {
				http.Error(w, "Cannot stage uploaded chunk", 500)
				return
			}
			hasher.Write(chunk)
			remaining -= size
		}
		var extra [1]byte
		if n, err := file.Read(extra[:]); n != 0 || err != io.EOF {
			http.Error(w, "Uploaded size mismatch", 400)
			return
		}
		if hex.EncodeToString(hasher.Sum(nil)) != metadata.FileHash {
			http.Error(w, "Uploaded file hash mismatch", 409)
			return
		}
		if _, err := reader.NextPart(); err != io.EOF {
			http.Error(w, "Unexpected multipart content", 400)
			return
		}
		if apply == nil {
			http.Error(w, "File application unavailable", 503)
			return
		}
		if err := apply(metadata, transferRoot); err != nil {
			var keep *KeepLocalError
			var conflict *SyncConflictError
			if errors.As(err, &keep) {
				writeTransferJSON(w, TransferCompletion{KeepLocal: true})
				return
			}
			if errors.As(err, &conflict) {
				writeTransferJSON(w, TransferCompletion{ConflictFile: conflict.ConflictFile})
				return
			}
			http.Error(w, fmt.Sprintf("Apply verified file: %v", err), 500)
			return
		}
		//Rabindra Neupane
		if err := cacheVerifiedTransfer(root, transferRoot, metadata); err != nil {
			http.Error(w, fmt.Sprintf("Cache verified chunks: %v", err), 500)
			return
		}
		writeTransferJSON(w, TransferCompletion{Completed: true, FileHash: metadata.FileHash})
	})
}

//Rabindra Neupane
// Reuse only verified bytes after acceptance; conflict/keep-local never replace cache.
func cacheVerifiedTransfer(root, staging string, metadata FileMetadata) error {
	folder := filepath.Join(root, "File Chunks", metadata.FileName)
	if err := os.MkdirAll(folder, 0755); err != nil {
		return err
	}
	for index := range metadata.ChunkHashes {
		name := fmt.Sprintf("chunk_%d.chunk", index)
		data, err := readVerifiedChunk(filepath.Join(staging, metadata.FileName, name), metadata, index)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(folder, name), data, 0644); err != nil {
			return err
		}
	}
	return nil
}
