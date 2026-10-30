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
	transferMutex          sync.Mutex             // Prevent overlapping outgoing transfers from this app.
	fileProcessingMutex    sync.Mutex             // Serialize watcher processing with remote apply.
	remoteFileHashes       map[string]string      // Verified remote contents, guarded by fileProcessingMutex.
	deviceRegistry         *network.DeviceRegistry
	networkCancel          context.CancelFunc
}

// Rabindra Neupane
// SyncResult reports chunk content bytes, excluding metadata and HTTP overhead.
type SyncResult struct {
	KeepLocal      bool   `json:"keepLocal,omitempty"`
	BaseUpdated    bool   `json:"baseUpdated"`
	TotalChunks    int    `json:"totalChunks"`
	ChunksSent     int    `json:"chunksSent"`
	ChunkBytesSent int64  `json:"chunkBytesSent"`
	ConflictFile   string `json:"conflictFile,omitempty"`
}

// Rabindra Neupane
// Chunking implementation: Nisham Karki
// Version metadata implementation: Santosh Tripathee
// Incremental network integration: Rabindra Neupane
func (app *App) SyncFile(address, fileName string) (*SyncResult, error) {
	return app.sendPreparedFile(address, fileName, false)
}

// Rabindra Neupane
// Both transfer modes use Santosh's conflict metadata and verified acknowledgements.
func (app *App) sendPreparedFile(address, fileName string, whole bool) (*SyncResult, error) {
	if !app.transferMutex.TryLock() {
		return nil, fmt.Errorf("another file transfer is already running")
	}
	defer app.transferMutex.Unlock()
	if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, "/\\:\x00") {
		return nil, fmt.Errorf("select a valid file name")
	}
	// Release the processing lock before network I/O to allow reverse transfers.
	app.fileProcessingMutex.Lock()
	chunks, metadata, filePreparationError := app.prepareFileForSync(fileName)
	app.fileProcessingMutex.Unlock()
	if filePreparationError != nil {
		return nil, filePreparationError
	}
	if metadata.DeviceID == "" {
		return nil, fmt.Errorf("register this device with a coordinator before sending files")
	}
	requestContext := app.ctx
	if requestContext == nil {
		requestContext = context.Background()
	}
	result := &SyncResult{TotalChunks: len(chunks)}
	if whole {
		data := make([]byte, 0, metadata.FileSize)
		for _, chunk := range chunks {
			data = append(data, chunk.Data...)
		}
		fileSendingError := network.SendFile(requestContext, address, metadata, data)
		result.ChunksSent, result.ChunkBytesSent = len(chunks), int64(len(data))
		return app.finishOutgoingSync(fileName, metadata, result, fileSendingError)
	}
	transferPlan, transferStartError := network.StartChunkTransfer(requestContext, address, metadata)
	if transferStartError != nil {
		return nil, fmt.Errorf("start incremental sync: %w", transferStartError)
	}
	for _, index := range transferPlan.NeededChunks {
		chunk := chunks[index]
		if chunkSendingError := network.SendTransferChunk(requestContext, address, transferPlan.TransferID, index, chunk.Data); chunkSendingError != nil {
			return nil, fmt.Errorf("send chunk %d: %w", index, chunkSendingError)
		}
		result.ChunksSent++
		result.ChunkBytesSent += int64(len(chunk.Data))
	}
	// Even an unchanged or empty file needs explicit receiver verification.
	transferCompletionError := network.CompleteChunkTransfer(requestContext, address, transferPlan.TransferID, metadata.FileHash)
	return app.finishOutgoingSync(fileName, metadata, result, transferCompletionError)
}

// Rabindra Neupane
// Never advance a conflict/keep-local base or a sender edited since the snapshot.
func (app *App) finishOutgoingSync(fileName string, metadata network.FileMetadata, result *SyncResult, outgoingTransferError error) (*SyncResult, error) {
	if outgoingTransferError != nil {
		var syncConflictError *network.SyncConflictError
		var keepLocalError *network.KeepLocalError
		if errors.As(outgoingTransferError, &syncConflictError) {
			result.ConflictFile = syncConflictError.ConflictFile
			return result, nil
		}
		if errors.As(outgoingTransferError, &keepLocalError) {
			result.KeepLocal = true
			return result, nil
		}
		return nil, fmt.Errorf("complete verified sync: %w", outgoingTransferError)
	}
	app.fileProcessingMutex.Lock()
	defer app.fileProcessingMutex.Unlock()
	currentFileHash, fileHashingError := HashFileOnDisk(filepath.Join(app.syncFolderPath, fileName))
	if fileHashingError != nil {
		return nil, fileHashingError
	}
	var version int
	if versionQueryError := app.db.QueryRow("SELECT current_version FROM files WHERE id=?", fileName).Scan(&version); versionQueryError != nil {
		return nil, versionQueryError
	}
	if currentFileHash == metadata.FileHash && version == metadata.Version {
		if baseHashSavingError := SetBaseHash(app.db, fileName, metadata.FileHash); baseHashSavingError != nil {
			return nil, fmt.Errorf("receiver verified file but saving sender base failed: %w", baseHashSavingError)
		}
		result.BaseUpdated = true
	}
	fmt.Printf("Verified sync of %s: %d/%d chunks, %d chunk data bytes (excluding metadata/HTTP)\n", fileName, result.ChunksSent, result.TotalChunks, result.ChunkBytesSent)
	return result, nil
}

// Rabindra Neupane
// Caller holds fileProcessingMutex. Chunking/storage: Nisham Karki.
// Version recording: Santosh Tripathee. Preparation coordination: Rabindra Neupane.
func (app *App) prepareFileForSync(fileName string) ([]Chunk, network.FileMetadata, error) {
	var metadata network.FileMetadata
	if app.db == nil {
		return nil, metadata, fmt.Errorf("file metadata database is unavailable")
	}
	path := filepath.Join(app.syncFolderPath, fileName)
	if fileReadyError := waitForFileReady(path); fileReadyError != nil {
		return nil, metadata, fileReadyError
	}
	fileInformationBefore, initialFileStatError := os.Stat(path)
	if initialFileStatError != nil {
		return nil, metadata, initialFileStatError
	}
	chunks, fileChunkingError := app.ChunkFile(path)
	if fileChunkingError != nil {
		return nil, metadata, fileChunkingError
	}
	if chunkSavingError := app.saveChunks(path, chunks); chunkSavingError != nil {
		return nil, metadata, fmt.Errorf("save local chunks: %w", chunkSavingError)
	}
	fileInformationAfter, secondFileStatError := os.Stat(path)
	if secondFileStatError != nil {
		return nil, metadata, secondFileStatError
	}
	if !os.SameFile(fileInformationBefore, fileInformationAfter) || fileInformationBefore.Size() != fileInformationAfter.Size() || !fileInformationBefore.ModTime().Equal(fileInformationAfter.ModTime()) {
		return nil, metadata, fmt.Errorf("file changed during preparation; finish saving and try Sync Now again")
	}
	deviceID := ""
	if app.deviceRegistry != nil {
		deviceID = app.deviceRegistry.Status().DeviceID
	}
	if versionRecordingError := RecordFileVersion(app.db, app.syncFolderPath, path, deviceID, chunks); versionRecordingError != nil {
		return nil, metadata, fmt.Errorf("record local file version: %w", versionRecordingError)
	}
	metadata, metadataBuildingError := app.metadataForSync(fileName, chunks)
	return chunks, metadata, metadataBuildingError
}

// Rabindra Neupane
// Read Santosh's existing version records without changing the database schema
// after serialized preparation records the current content.
func (app *App) metadataForSync(fileName string, chunks []Chunk) (network.FileMetadata, error) {
	metadata := network.FileMetadata{FileID: fileName, FileName: fileName, ChunkSize: chunkSize, ChunkHashes: make([]string, len(chunks))}
	hasher := sha256.New()
	for index, chunk := range chunks {
		metadata.ChunkHashes[index] = chunk.Hash
		metadata.FileSize += int64(len(chunk.Data))
		hasher.Write(chunk.Data)
	}
	metadata.FileHash = hex.EncodeToString(hasher.Sum(nil))
	if app.db == nil {
		return metadata, fmt.Errorf("file metadata database is unavailable")
	}
	baseHash, baseHashReadingError := GetBaseHash(app.db, fileName)
	if baseHashReadingError != nil {
		return metadata, baseHashReadingError
	}
	metadata.BaseHash = baseHash
	if app.deviceRegistry != nil {
		metadata.DeviceID = app.deviceRegistry.Status().DeviceID
	}
	databaseTransaction, transactionStartError := app.db.Begin()
	if transactionStartError != nil {
		return metadata, transactionStartError
	}
	defer databaseTransaction.Rollback()
	var versionID, recordedSize int64
	var base sql.NullInt64
	versionReadingError := databaseTransaction.QueryRow(`SELECT v.id, v.version_number, v.based_on_version, f.size
		FROM files f JOIN file_versions v ON v.file_id = f.id AND v.version_number = f.current_version
		WHERE f.id = ? ORDER BY v.id DESC LIMIT 1`, fileName).Scan(&versionID, &metadata.Version, &base, &recordedSize)
	if versionReadingError == sql.ErrNoRows {
		return metadata, fmt.Errorf("file is still being processed; wait a moment and try Sync Now again")
	}
	if versionReadingError != nil {
		return metadata, fmt.Errorf("read file version: %w", versionReadingError)
	}
	if base.Valid {
		value := int(base.Int64)
		metadata.BaseVersion = &value
		baseChunkHashes, baseChunkReadingError := loadBaseChunkHashes(databaseTransaction, fileName, value)
		if baseChunkReadingError != nil {
			return metadata, fmt.Errorf("read base version identity: %w", baseChunkReadingError)
		}
		metadata.BaseChunkHashes = baseChunkHashes
		baseRevisions, revisionHistoryReadingError := loadBaseRevisionHistory(databaseTransaction, fileName, value)
		if revisionHistoryReadingError != nil {
			return metadata, fmt.Errorf("read base revision history: %w", revisionHistoryReadingError)
		}
		metadata.BaseRevisions = baseRevisions
	}
	chunkRows, chunkQueryError := databaseTransaction.Query("SELECT chunk_index, chunk_hash FROM chunks WHERE file_version_id = ? ORDER BY chunk_index", versionID)
	if chunkQueryError != nil {
		return metadata, chunkQueryError
	}
	defer chunkRows.Close()
	count := 0
	for chunkRows.Next() {
		var index int
		var hash string
		if chunkScanningError := chunkRows.Scan(&index, &hash); chunkScanningError != nil {
			return metadata, chunkScanningError
		}
		if count >= len(chunks) || index != count || hash != chunks[count].Hash {
			return metadata, fmt.Errorf("file version is still being processed; wait a moment and try Sync Now again")
		}
		count++
	}
	if chunkIterationError := chunkRows.Err(); chunkIterationError != nil {
		return metadata, chunkIterationError
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

func (app *App) startup(ctx context.Context) {
	app.ctx = ctx

	// Initializes the map that is used to manage file processing timers.
	app.fileTimers = make(map[string]*time.Timer)

	// Get the project's current directory
	getProjectDirectory, projectDirectoryError := os.Getwd()

	// Print any project directory error
	if projectDirectoryError != nil {
		fmt.Println("Error getting project directory: ", projectDirectoryError)
	}

	// Create path to save files inside a dedicated synced files folder
	app.syncFolderPath = filepath.Join(
		getProjectDirectory,
		"Synced Files",
	)

	// Print output to test where the files are being saved
	fmt.Println("Sync folder path:", app.syncFolderPath)

	// Create the folder if it doesn't exist
	// Create "Synced Files" folder
	// 0755: File Permission.
	// The file owner (Owner) can read, write, and execute,
	// while the group and others can read and execute but cannot write
	os.MkdirAll(app.syncFolderPath, 0755)

	// Create path to save chunks inside "File Chunks" folder
	app.chunkStoragePath = filepath.Join(
		getProjectDirectory,
		"File Chunks",
	)

	// Create "File Chunks" folder, if it does not exist
	os.MkdirAll(app.chunkStoragePath, 0755)

	// Create path used to store reconstructed files.
	app.reconstructedFilesPath = filepath.Join(
		getProjectDirectory,
		"Reconstructed Files",
	)

	// Create the Reconstructed Files folder if it does not already exist.
	os.MkdirAll(
		app.reconstructedFilesPath,
		0755,
	)

	// Initialize the metadata database
	database, databaseInitializationError := InitDatabase(app.syncFolderPath)

	if databaseInitializationError != nil {
		fmt.Println("Database setup failed:", databaseInitializationError)
		return
	}
	app.db = database
	serviceCtx, cancel := context.WithCancel(ctx)
	app.networkCancel = cancel
	deviceRegistry, deviceRegistryOpeningError := network.OpenDeviceRegistry(serviceCtx, database, getProjectDirectory, 8080)
	app.deviceRegistry = deviceRegistry
	if deviceRegistryOpeningError != nil {
		fmt.Println("Device registration setup failed:", deviceRegistryOpeningError)
	}

	// Initialize FileWatcher function and start monitoring "Synced Files" folder at startup
	app.FileWatcher(app.syncFolderPath)

	// Start the local HTTP Server
	// Reconstruction implementation: Nisham Karki
	go network.StartServer(serviceCtx, "8080", app.completeIncomingTransfer, app.deviceRegistry)
}

// Rabindra Neupane
func (app *App) ConfigureCoordinator(coordinator bool, address string) error {
	if app.deviceRegistry == nil {
		return fmt.Errorf("device registration is unavailable; check startup logs")
	}
	return app.deviceRegistry.Configure(coordinator, address)
}

// Rabindra Neupane
func (app *App) GetRegistrationStatus() (network.RegistrationStatus, error) {
	if app.deviceRegistry == nil {
		return network.RegistrationStatus{}, fmt.Errorf("device registration is unavailable; check startup logs")
	}
	return app.deviceRegistry.Status(), nil
}

// Rabindra Neupane
func (app *App) GetRegisteredDevices() ([]network.Device, error) {
	if app.deviceRegistry == nil {
		return nil, fmt.Errorf("device registration is unavailable")
	}
	return app.deviceRegistry.Devices()
}

// Rabindra Neupane
func (app *App) shutdown(ctx context.Context) {
	if app.networkCancel != nil {
		app.networkCancel()
	}
	if app.deviceRegistry != nil {
		app.deviceRegistry.Close()
	}
	if app.fileWatcher != nil {
		app.fileWatcher.Close()
	}
	app.timerMutex.Lock()
	for _, timer := range app.fileTimers {
		timer.Stop()
	}
	app.timerMutex.Unlock()
}

// Rabindra Neupane
// Reconstruction implementation: Nisham Karki
// Network integration: Rabindra Neupane
func (app *App) completeIncomingTransfer(metadata network.FileMetadata, chunkRoot string) error {
	return app.applyIncomingTransfer(metadata, chunkRoot)
}

// /Rabindra Neupane
// PingDevice pings a device to check whether another FileSync device
// can be reached at the specified address
func (app *App) PingDevice(address string) (network.PingResponse, error) {
	// Send a ping request to other device
	pingResponse, pingError := network.PingDevice(address)

	// Check for any error during ping
	if pingError != nil {
		return network.PingResponse{}, pingError
	}

	return *pingResponse, nil
}

// /Rabindra Neupane
// GetLocalIP retrieves the local IP address of the current device
func (app *App) GetLocalIP() (string, error) {
	return network.GetLocalIP()
}

// Rabindra Neupane
// DiscoverDevices discovers devices on the local network
func (app *App) DiscoverDevices() []network.Device {
	return network.DiscoverDevices()
}

// /Rabindra Neupane
// SendFile sends a file to another FileSync device on the network
func (app *App) SendFile(address string, fileName string) (*SyncResult, error) {
	return app.sendPreparedFile(address, fileName, true)
}
