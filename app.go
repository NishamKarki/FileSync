package main

import (
	"FileSyncWails/network"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// // App stores the shared application state used by the FileSync backend.
type App struct {
	ctx                    context.Context
	syncFolderPath         string            // Folder path containing Files monitored by FileSync
	fileWatcher            *fsnotify.Watcher // Watcher used to detect change inside the sync folder
	chunkStoragePath       string            // Path where the generated file chunks are store
	reconstructedFilesPath string
	db                     *sql.DB
	fileTimers             map[string]*time.Timer // Stores tier for files that are being modified
	timerMutex             sync.Mutex             // Protects fileTimer from being accessed by multiple go routine at the same time
	//Rabindra Neupane
	transferMutex sync.Mutex // Prevent overlapping outgoing transfers from this app.
	//Rabindra Neupane
	fileProcessingMutex sync.Mutex        // Serialize watcher processing with remote apply.
	remoteFileHashes    map[string]string // Verified remote contents, guarded by fileProcessingMutex.
	//Rabindra Neupane
	deviceRegistry *network.DeviceRegistry
	networkCancel  context.CancelFunc
}

//Rabindra Neupane
// SyncResult reports chunk content bytes, excluding metadata and HTTP overhead.
type SyncResult struct {
	TotalChunks    int   `json:"totalChunks"`
	ChunksSent     int   `json:"chunksSent"`
	ChunkBytesSent int64 `json:"chunkBytesSent"`
	//Rabindra Neupane
	ConflictFile string `json:"conflictFile,omitempty"`
}

//Rabindra Neupane
// Chunking implementation: Nisham Karki
// Version metadata implementation: Santosh Tripathee
// Incremental network integration: Rabindra Neupane
func (a *App) SyncFile(address, fileName string) (*SyncResult, error) {
	if !a.transferMutex.TryLock() {
		return nil, fmt.Errorf("another file transfer is already running")
	}
	defer a.transferMutex.Unlock()
	if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, "/\\:\x00") {
		return nil, fmt.Errorf("select a valid file name")
	}
	chunks, err := a.ChunkFile(filepath.Join(a.syncFolderPath, fileName))
	if err != nil {
		return nil, err
	}
	metadata, err := a.metadataForSync(fileName, chunks)
	if err != nil {
		return nil, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	plan, err := network.StartChunkTransfer(ctx, address, metadata)
	if err != nil {
		return nil, fmt.Errorf("start incremental sync: %w", err)
	}
	result := &SyncResult{TotalChunks: len(chunks)}
	for _, index := range plan.NeededChunks {
		chunk := chunks[index]
		if err := network.SendTransferChunk(ctx, address, plan.TransferID, index, chunk.Data); err != nil {
			return nil, fmt.Errorf("send chunk %d: %w", index, err)
		}
		result.ChunksSent++
		result.ChunkBytesSent += int64(len(chunk.Data))
	}
	// Even an unchanged or empty file needs explicit receiver verification.
	if err := network.CompleteChunkTransfer(ctx, address, plan.TransferID); err != nil {
		//Rabindra Neupane
		var conflict *network.SyncConflictError
		if errors.As(err, &conflict) {
			result.ConflictFile = conflict.ConflictFile
			return result, nil
		}
		return nil, fmt.Errorf("complete incremental sync: %w", err)
	}
	fmt.Printf("Verified sync of %s: %d/%d chunks, %d chunk data bytes (excluding metadata/HTTP)\n", fileName, result.ChunksSent, result.TotalChunks, result.ChunkBytesSent)
	return result, nil
}

//Rabindra Neupane
// Read Santosh's existing version records without changing the database schema
// or recording a second version alongside the file watcher.
func (a *App) metadataForSync(fileName string, chunks []Chunk) (network.FileMetadata, error) {
	metadata := network.FileMetadata{FileID: fileName, FileName: fileName, ChunkSize: chunkSize, ChunkHashes: make([]string, len(chunks))}
	hasher := sha256.New()
	for index, chunk := range chunks {
		metadata.ChunkHashes[index] = chunk.Hash
		metadata.FileSize += int64(len(chunk.Data))
		hasher.Write(chunk.Data)
	}
	metadata.FileHash = hex.EncodeToString(hasher.Sum(nil))
	if a.db == nil {
		return metadata, fmt.Errorf("file metadata database is unavailable")
	}
	tx, err := a.db.Begin()
	if err != nil {
		return metadata, err
	}
	defer tx.Rollback()
	var versionID, recordedSize int64
	var base sql.NullInt64
	err = tx.QueryRow(`SELECT v.id, v.version_number, v.based_on_version, f.size
		FROM files f JOIN file_versions v ON v.file_id = f.id AND v.version_number = f.current_version
		WHERE f.id = ? ORDER BY v.id DESC LIMIT 1`, fileName).Scan(&versionID, &metadata.Version, &base, &recordedSize)
	if err == sql.ErrNoRows {
		return metadata, fmt.Errorf("file is still being processed; wait a moment and try Sync Now again")
	}
	if err != nil {
		return metadata, fmt.Errorf("read file version: %w", err)
	}
	if base.Valid {
		value := int(base.Int64)
		metadata.BaseVersion = &value
		//Rabindra Neupane
		metadata.BaseChunkHashes, err = loadBaseChunkHashes(tx, fileName, value)
		if err != nil {
			return metadata, fmt.Errorf("read base version identity: %w", err)
		}
		//Rabindra Neupane
		metadata.BaseRevisions, err = loadBaseRevisionHistory(tx, fileName, value)
		if err != nil {
			return metadata, fmt.Errorf("read base revision history: %w", err)
		}
	}
	rows, err := tx.Query("SELECT chunk_index, chunk_hash FROM chunks WHERE file_version_id = ? ORDER BY chunk_index", versionID)
	if err != nil {
		return metadata, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var index int
		var hash string
		if err := rows.Scan(&index, &hash); err != nil {
			return metadata, err
		}
		if count >= len(chunks) || index != count || hash != chunks[count].Hash {
			return metadata, fmt.Errorf("file version is still being processed; wait a moment and try Sync Now again")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return metadata, err
	}
	if count != len(chunks) || recordedSize != metadata.FileSize {
		return metadata, fmt.Errorf("file version is still being processed; wait a moment and try Sync Now again")
	}
	return metadata, nil
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the wails application starts.
// Initializes main folders, database, file watcher, timer storage, and local network server

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initializes the map that is used to manage file processing timers.
	a.fileTimers = make(map[string]*time.Timer)

	// Get the project's current directory
	getProjectDirectory, projectDirectoryError := os.Getwd()

	// Print any project directory error
	if projectDirectoryError != nil {
		fmt.Println("Error getting project directory: ", projectDirectoryError)
	}

	// Create path to save files inside a dedicated synced files folder
	a.syncFolderPath = filepath.Join(
		getProjectDirectory,
		"Synced Files",
	)

	// Print output to test where the files are being saved
	fmt.Println("Sync folder path:", a.syncFolderPath)

	// Create the folder if it doesn't exist
	// Create "Synced Files" folder
	// 0755: File Permission.
	// The file owner (Owner) can read, write, and execute,
	// while the group and others can read and execute but cannot write
	os.MkdirAll(a.syncFolderPath, 0755)

	// Create path to save chunks inside "File Chunks" folder
	a.chunkStoragePath = filepath.Join(
		getProjectDirectory,
		"File Chunks",
	)

	// Create "File Chunks" folder, if it does not exist
	os.MkdirAll(a.chunkStoragePath, 0755)

	// Create path used to store reconstructed files.
	a.reconstructedFilesPath = filepath.Join(
		getProjectDirectory,
		"Reconstructed Files",
	)

	// Create the Reconstructed Files folder if it does not already exist.
	os.MkdirAll(
		a.reconstructedFilesPath,
		0755,
	)

	// Initialize the metadata database
	db, dbError := InitDatabase(a.syncFolderPath)

	if dbError != nil {
		fmt.Println("Database setup failed:", dbError)
		return
	}
	a.db = db
	//Rabindra Neupane
	serviceCtx, cancel := context.WithCancel(ctx)
	a.networkCancel = cancel
	a.deviceRegistry, dbError = network.OpenDeviceRegistry(serviceCtx, db, getProjectDirectory, 8080)
	if dbError != nil {
		fmt.Println("Device registration setup failed:", dbError)
	}

	// Initialize FileWatcher function and start monitoring "Synced Files" folder at startup
	a.FileWatcher(a.syncFolderPath)

	// Start the local HTTP Server
	//Rabindra Neupane
	// Reconstruction implementation: Nisham Karki
	// Network integration: Rabindra Neupane
	go network.StartServer(serviceCtx, "8080", a.ReconstructFile, a.completeIncomingTransfer, a.deviceRegistry)
}

//Rabindra Neupane
func (a *App) ConfigureCoordinator(coordinator bool, address string) error {
	if a.deviceRegistry == nil {
		return fmt.Errorf("device registration is unavailable; check startup logs")
	}
	return a.deviceRegistry.Configure(coordinator, address)
}

//Rabindra Neupane
func (a *App) GetRegistrationStatus() (network.RegistrationStatus, error) {
	if a.deviceRegistry == nil {
		return network.RegistrationStatus{}, fmt.Errorf("device registration is unavailable; check startup logs")
	}
	return a.deviceRegistry.Status(), nil
}

//Rabindra Neupane
func (a *App) GetRegisteredDevices() ([]network.Device, error) {
	if a.deviceRegistry == nil {
		return nil, fmt.Errorf("device registration is unavailable")
	}
	return a.deviceRegistry.Devices()
}

//Rabindra Neupane
func (a *App) shutdown(ctx context.Context) {
	if a.networkCancel != nil {
		a.networkCancel()
	}
	if a.deviceRegistry != nil {
		a.deviceRegistry.Close()
	}
	if a.fileWatcher != nil {
		a.fileWatcher.Close()
	}
	a.timerMutex.Lock()
	for _, timer := range a.fileTimers {
		timer.Stop()
	}
	a.timerMutex.Unlock()
}

//Rabindra Neupane
// Reconstruction implementation: Nisham Karki
// Network integration: Rabindra Neupane
func (a *App) completeIncomingTransfer(metadata network.FileMetadata, chunkRoot string) error {
	//Rabindra Neupane
	return a.applyIncomingTransfer(metadata, chunkRoot)
}

// /Rabindra Neupane
// PingDevice pings a device to check whether another FileSync device
// can be reached at the specified address
func (a *App) PingDevice(address string) (network.PingResponse, error) {
	// Send a ping request to other device
	response, err := network.PingDevice(address)

	// Check for any error during ping
	if err != nil {
		return network.PingResponse{}, err
	}

	return *response, nil
}

// /Rabindra Neupane
// GetLocalIP retrieves the local IP address of the current device
func (a *App) GetLocalIP() (string, error) {
	return network.GetLocalIP()
}

//Rabindra Neupane
// DiscoverDevices discovers devices on the local network
func (a *App) DiscoverDevices() []network.Device {
	return network.DiscoverDevices()
}

// /Rabindra Neupane
// SendFile sends a file to another FileSync device on the network
func (a *App) SendFile(address string, fileName string) error {
	//Rabindra Neupane
	if !a.transferMutex.TryLock() {
		return fmt.Errorf("another file transfer is already running")
	}
	defer a.transferMutex.Unlock()
	// Create the path of the file that will be sent
	filePath := filepath.Join(a.syncFolderPath, fileName)

	return network.SendFile(address, filePath)
}

//Rabindra Neupane
// Removed the obsolete first-chunk experiment; retain full and incremental transfers.
//Rabindra Neupane
// SendAllChunks sends all chunks of a file to another FileSync device on the network
func (a *App) SendAllChunks(address string, fileName string) error {
	//Rabindra Neupane
	if !a.transferMutex.TryLock() {
		return fmt.Errorf("another file transfer is already running")
	}
	defer a.transferMutex.Unlock()
	filePath := filepath.Join(a.syncFolderPath, fileName)

	// Chunk the file into smaller pieces for sending
	chunks, err := a.ChunkFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to chunk file %s: %w", filePath, err)
	}

	// Check if any chunks were created for the file
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks created for file: %s", filePath)
	}
	// Send all chunks of the file to the specified address
	fmt.Printf("Sending %d chunks of %s to %s\n", len(chunks), fileName, address)

	// Send chunk one by one, ensuring each chunk is successfully transmitted before moving on to the next one
	for _, chunk := range chunks {
		fmt.Printf("Sending chunk %d of %d...\n", chunk.Index+1, len(chunks))
		err := network.SendChunk(address, fileName, chunk.Index, len(chunks), chunk.Hash, chunk.Data)
		if err != nil {
			return fmt.Errorf("failed to send chunk %d of %s: %w", chunk.Index, fileName, err)
		}
	}

	fmt.Printf("All %d chunks of %s successfully sent to %s\n", len(chunks), fileName, address)
	return nil
}
