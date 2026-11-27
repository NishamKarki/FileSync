package main

import (
	"FileSyncWails/network"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// // App stores the shared application state used by the FileSync backend.
type App struct {
	ctx                    context.Context
	syncFolderPath         string            // Folder path containing Files monitored by FileSync
	fileWatcher            *fsnotify.Watcher // Watcher used to detect change inside the sync folder
	chunkStoragePath       string            // Path where the generated file chunks are store
	reconstructedFilesPath string
	db                     *sql.DB
	chunkSize              int                    // Active fixed chunk size in bytes (defaults to 4 KiB)
	autoSyncEnabled        bool                   // When true, file watcher pushes changes automatically
	autoSyncTarget         string                 // Peer address or "all" for automatic sync
	autoSyncMutex          sync.RWMutex           // Protects autoSyncEnabled and autoSyncTarget
	fileTimers             map[string]*time.Timer // Stores tier for files that are being modified
	timerMutex             sync.Mutex             // Protects fileTimer from being accessed by multiple go routine at the same time
	transferMutex          sync.Mutex             // Prevent overlapping outgoing transfers from this app.
	fileProcessingMutex    sync.Mutex             // Serialize watcher processing with remote apply.
	remoteFileHashes       map[string]string      // Verified remote contents, guarded by fileProcessingMutex.
	deviceRegistry         *network.DeviceRegistry
	chunkIndex             *network.ChunkFileIndex
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
// SyncFile sends only the chunks requested by the selected receiver.
func (app *App) SyncFile(address, fileName string) (*SyncResult, error) {
	// Start the normal incremental transfer workflow.
	return app.sendPreparedFile(address, fileName, false)
}

// Rabindra Neupane
// sendPreparedFile prepares one stable snapshot and runs incremental or benchmark transfer.
// Both modes use Santosh's conflict metadata and verified acknowledgements.
func (app *App) sendPreparedFile(address, fileName string, whole bool) (syncResult *SyncResult, syncError error) {
	// Lock transfer work so two outgoing syncs cannot mix progress and state.
	if !app.transferMutex.TryLock() {
		return nil, fmt.Errorf("another file transfer is already running")
	}
	defer app.transferMutex.Unlock()
	if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, "/\\:\x00") {
		return nil, fmt.Errorf("select a valid file name")
	}
	// Santosh Tripathee: start measuring the complete synchronization workflow.
	cpuTimeAtStart := processCPUTime()
	prepareStart := time.Now()
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
	// Santosh Tripathee: record metrics after every completed or failed transfer.
	prepareTime := time.Since(prepareStart)
	transferStart := time.Now()
	defer func() {
		app.recordSyncPerformance(fileName, whole, metadata.FileSize, metadata.ChunkSize, len(chunks),
			prepareTime, transferStart, cpuTimeAtStart, syncResult, syncError)
	}()
	requestContext := app.ctx
	if requestContext == nil {
		requestContext = context.Background()
	}
	result := &SyncResult{TotalChunks: len(chunks)}
	transferClient := app.deviceRegistry.TransferHTTPClient()
	if whole {
		data := make([]byte, 0, metadata.FileSize)
		for _, chunk := range chunks {
			data = append(data, chunk.Data...)
		}
		fileSendingError := network.SendFile(requestContext, address, metadata, data, transferClient)
		result.ChunksSent, result.ChunkBytesSent = len(chunks), int64(len(data))
		return app.finishOutgoingSync(fileName, metadata, result, fileSendingError)
	}
	transferPlan, transferStartError := network.StartChunkTransfer(requestContext, address, metadata, transferClient)
	if transferStartError != nil {
		return nil, fmt.Errorf("start incremental sync: %w", transferStartError)
	}
	if chunkSendingError := sendNeededChunks(requestContext, transferClient, address, transferPlan.TransferID, transferPlan.NeededChunks, chunks, result); chunkSendingError != nil {
		return nil, chunkSendingError
	}
	// Even an unchanged or empty file needs explicit receiver verification.
	transferCompletionError := network.CompleteChunkTransfer(requestContext, address, transferPlan.TransferID, metadata.FileHash, transferClient)
	return app.finishOutgoingSync(fileName, metadata, result, transferCompletionError)
}

func sendNeededChunks(requestContext context.Context, transferClient *http.Client, address, transferID string, needed []int, chunks []Chunk, result *SyncResult) error {
	if len(needed) == 0 {
		return nil
	}
	workers := 6
	if len(needed) < workers {
		workers = len(needed)
	}
	uploadCtx, cancelUploads := context.WithCancel(requestContext)
	defer cancelUploads()

	jobs := make(chan int, len(needed))
	for _, index := range needed {
		jobs <- index
	}
	close(jobs)

	var sentCount atomic.Int64
	var sentBytes atomic.Int64
	var firstErr error
	var errMutex sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if uploadCtx.Err() != nil {
					return
				}
				chunk := chunks[index]
				if err := network.SendTransferChunk(uploadCtx, address, transferID, index, chunk.Data, transferClient); err != nil {
					errMutex.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("send chunk %d: %w", index, err)
						cancelUploads()
					}
					errMutex.Unlock()
					return
				}
				sentCount.Add(1)
				sentBytes.Add(int64(len(chunk.Data)))
			}
		}()
	}
	wg.Wait()
	result.ChunksSent = int(sentCount.Load())
	result.ChunkBytesSent = sentBytes.Load()
	return firstErr
}

// Rabindra Neupane
// finishOutgoingSync handles the receiver result and advances only a verified shared base.
// Never advance a conflict, keep-local result, or file edited since the snapshot.
func (app *App) finishOutgoingSync(fileName string, metadata network.FileMetadata, result *SyncResult, outgoingTransferError error) (*SyncResult, error) {
	// Check whether the receiver reported a conflict, newer file, or transfer failure.
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
// prepareFileForSync records and returns one stable outgoing file snapshot.
func (app *App) prepareFileForSync(fileName string) ([]Chunk, network.FileMetadata, error) {
	// Create an empty result used when preparation fails.
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
	if metadataBuildingError == nil {
		// Report the verified local cache so peers can locate these chunks later.
		app.scheduleLocalChunkInventoryReport()
	}
	return chunks, metadata, metadataBuildingError
}

// Rabindra Neupane
// metadataForSync reads Santosh's version records and creates network metadata.
// Serialized preparation records the current content before this read.
func (app *App) metadataForSync(fileName string, chunks []Chunk) (network.FileMetadata, error) {
	activeChunkSize := int64(app.effectiveChunkSize())
	// Create metadata with ordered space for every current chunk hash.
	metadata := network.FileMetadata{FileID: fileName, FileName: fileName, ChunkSize: activeChunkSize, ChunkHashes: make([]string, len(chunks))}
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
	versionReadingError := databaseTransaction.QueryRow(`SELECT v.id, v.version_number, f.size
		FROM files f JOIN file_versions v ON v.file_id = f.id AND v.version_number = f.current_version
		WHERE f.id = ? ORDER BY v.id DESC LIMIT 1`, fileName).Scan(&versionID, &metadata.Version, &recordedSize)
	if versionReadingError == sql.ErrNoRows {
		return metadata, fmt.Errorf("file is still being processed; wait a moment and try Sync Now again")
	}
	if versionReadingError != nil {
		return metadata, fmt.Errorf("read file version: %w", versionReadingError)
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
	// Create the application state before Wails startup fills its services.
	return &App{}
}

// startup is called when the wails application starts.
// Initializes main folders, database, file watcher, timer storage, and local network server

func (app *App) startup(ctx context.Context) {
	// Save the Wails context used by dialogs and frontend events.
	app.ctx = ctx

	// Initialize the map that is used to manage file processing timers.
	app.fileTimers = make(map[string]*time.Timer)
	if envChunk := strings.TrimSpace(os.Getenv("FILESYNC_CHUNK_SIZE")); envChunk != "" {
		if parsed, parseErr := strconv.Atoi(envChunk); parseErr == nil && parsed >= 1024 && parsed <= 16<<20 {
			app.chunkSize = parsed
		}
	}

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
	app.chunkIndex = network.NewChunkFileIndex(app.chunkStoragePath)

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
	if deviceRegistry != nil {
		deviceRegistry.SetChunkFileIndex(app.chunkIndex)
	}
	if deviceRegistryOpeningError != nil {
		fmt.Println("Device registration setup failed:", deviceRegistryOpeningError)
	}

	// Initialize FileWatcher function and start monitoring "Synced Files" folder at startup
	app.FileWatcher(app.syncFolderPath)

	// Start the local HTTP Server
	// Reconstruction implementation: Nisham Karki
	go network.StartServer(serviceCtx, "8080", app.completeIncomingTransfer, app.deviceRegistry, app.scheduleLocalChunkInventoryReport, app.prepareFileMetadataForPeer)
}

// Rabindra Neupane
// ConfigureCoordinator applies the coordinator role or remote address from the frontend.
func (app *App) ConfigureCoordinator(coordinator bool, address string, pairingCode string) error {
	// Check that startup created the device registry.
	if app.deviceRegistry == nil {
		return fmt.Errorf("device registration is unavailable; check startup logs")
	}
	// Save the new role or coordinator address.
	return app.deviceRegistry.Configure(coordinator, address, pairingCode)
}

// Rabindra Neupane
// StartCoordinatorPairing creates a temporary single-use code for one new client.
func (app *App) StartCoordinatorPairing() (network.PairingStatus, error) {
	// Check that startup created the device registry.
	if app.deviceRegistry == nil {
		return network.PairingStatus{}, fmt.Errorf("device registration is unavailable")
	}
	// Open the secure pairing window on the local coordinator.
	return app.deviceRegistry.StartPairing()
}

// Rabindra Neupane
// GetCoordinatorPairingStatus returns the local pairing window for display.
func (app *App) GetCoordinatorPairingStatus() (network.PairingStatus, error) {
	// Check that startup created the device registry.
	if app.deviceRegistry == nil {
		return network.PairingStatus{}, fmt.Errorf("device registration is unavailable")
	}
	// Return the current local pairing state.
	return app.deviceRegistry.GetPairingStatus(), nil
}

// Rabindra Neupane
// RemoveRegisteredDevice revokes a client credential on the coordinator.
func (app *App) RemoveRegisteredDevice(deviceID string) error {
	// Check that startup created the device registry.
	if app.deviceRegistry == nil {
		return fmt.Errorf("device registration is unavailable")
	}
	// Remove the selected device and invalidate its future signed requests.
	return app.deviceRegistry.RevokeDevice(deviceID)
}

// Rabindra Neupane
// GetRegistrationStatus returns this installation's public registration state.
func (app *App) GetRegistrationStatus() (network.RegistrationStatus, error) {
	// Check that registration is available before reading its status.
	if app.deviceRegistry == nil {
		return network.RegistrationStatus{}, fmt.Errorf("device registration is unavailable; check startup logs")
	}
	// Return a safe public snapshot to the frontend.
	return app.deviceRegistry.Status(), nil
}

// Rabindra Neupane
// GetRegisteredDevices returns the coordinator's current online and offline devices.
func (app *App) GetRegisteredDevices() ([]network.Device, error) {
	// Check that registration is available before requesting devices.
	if app.deviceRegistry == nil {
		return nil, fmt.Errorf("device registration is unavailable")
	}
	// Read devices locally or from the selected coordinator.
	return app.deviceRegistry.Devices()
}

// Rabindra Neupane
// shutdown stops network and registry services before closing the database.
func (app *App) shutdown(ctx context.Context) {
	// Stop the HTTP server before closing shared services.
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
// completeIncomingTransfer applies a receiver transfer after chunk verification.
func (app *App) completeIncomingTransfer(metadata network.FileMetadata, chunkRoot string) error {
	// Apply the verified staged chunks through receiver conflict handling.
	return app.applyIncomingTransfer(metadata, chunkRoot)
}

// Rabindra Neupane
// DiscoverDevices scans the local subnet for FileSync devices.
func (app *App) DiscoverDevices() []network.Device {
	// Scan the local subnet through the network package.
	return network.DiscoverDevices()
}

// Rabindra Neupane
// SendFile runs the whole-file benchmark transfer for the selected file.
func (app *App) SendFile(address string, fileName string) (*SyncResult, error) {
	// Start the whole-file benchmark workflow.
	return app.sendPreparedFile(address, fileName, true)
}

// SetAutoSync enables or disables automatic synchronization when files change.
func (app *App) SetAutoSync(enabled bool, targetAddress string) {
	app.autoSyncMutex.Lock()
	app.autoSyncEnabled = enabled
	app.autoSyncTarget = strings.TrimSpace(targetAddress)
	app.autoSyncMutex.Unlock()
}

// GetAutoSync reports whether automatic synchronization on file changes is enabled.
func (app *App) GetAutoSync() bool {
	app.autoSyncMutex.RLock()
	defer app.autoSyncMutex.RUnlock()
	return app.autoSyncEnabled
}

func (app *App) autoSyncChangedFile(fileName string) {
	app.autoSyncMutex.RLock()
	enabled := app.autoSyncEnabled
	target := app.autoSyncTarget
	app.autoSyncMutex.RUnlock()
	if !enabled || app.deviceRegistry == nil || !app.deviceRegistry.Status().Registered {
		return
	}

	var targets []string
	if target == "" || target == "all" {
		devices, err := app.deviceRegistry.Devices()
		if err != nil {
			return
		}
		ownID := app.deviceRegistry.Status().DeviceID
		for _, device := range devices {
			if device.DeviceOnline && device.DeviceID != ownID {
				targets = append(targets, net.JoinHostPort(device.DeviceIP, strconv.Itoa(device.DevicePort)))
			}
		}
	} else {
		targets = []string{target}
	}

	for _, addr := range targets {
		res, err := app.SyncFile(addr, fileName)
		if app.ctx != nil && app.ctx.Value("events") != nil {
			if err != nil {
				runtime.EventsEmit(app.ctx, "auto-sync-result", fmt.Sprintf("Auto-sync %s to %s failed: %v", fileName, addr, err))
			} else if res.ConflictFile != "" {
				runtime.EventsEmit(app.ctx, "auto-sync-result", fmt.Sprintf("Auto-sync %s to %s: conflict (%s)", fileName, addr, res.ConflictFile))
			} else if res.KeepLocal {
				runtime.EventsEmit(app.ctx, "auto-sync-result", fmt.Sprintf("Auto-sync %s to %s: peer has newer version", fileName, addr))
			} else {
				runtime.EventsEmit(app.ctx, "auto-sync-result", fmt.Sprintf("Auto-sync %s to %s: %d/%d chunks (%d bytes)", fileName, addr, res.ChunksSent, res.TotalChunks, res.ChunkBytesSent))
			}
		}
	}
}

func (app *App) prepareFileMetadataForPeer(fileName string) (network.FileMetadata, error) {
	if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, "/\\:\x00") {
		return network.FileMetadata{}, fmt.Errorf("invalid file name")
	}
	app.fileProcessingMutex.Lock()
	defer app.fileProcessingMutex.Unlock()
	_, metadata, err := app.prepareFileForSync(fileName)
	return metadata, err
}

// PullFile downloads only missing or changed chunks for fileName from a peer
// (with coordinator peer fallback) and applies the verified version locally.
func (app *App) PullFile(address string, fileName string) (*SyncResult, error) {
	if !app.transferMutex.TryLock() {
		return nil, fmt.Errorf("another file transfer is already running")
	}
	defer app.transferMutex.Unlock()
	if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, "/\\:\x00") {
		return nil, fmt.Errorf("select a valid file name")
	}
	requestContext := app.ctx
	if requestContext == nil {
		requestContext = context.Background()
	}
	transferClient := app.deviceRegistry.TransferHTTPClient()
	chunkClient := app.deviceRegistry.ChunkRetrievalHTTPClient()
	metadata, err := network.FetchFileMetadata(requestContext, address, fileName, transferClient)
	if err != nil {
		return nil, fmt.Errorf("fetch peer metadata: %w", err)
	}
	stagingParent := filepath.Join(app.chunkStoragePath, ".transfers")
	if err := os.MkdirAll(stagingParent, 0755); err != nil {
		return nil, err
	}
	stagingRoot, err := os.MkdirTemp(stagingParent, strings.ReplaceAll(fileName, "*", "_")+"-pull-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stagingRoot)

	stagedFileDir := filepath.Join(stagingRoot, fileName)
	if err := os.MkdirAll(stagedFileDir, 0755); err != nil {
		return nil, err
	}

	result := &SyncResult{TotalChunks: len(metadata.ChunkHashes)}
	for index, expectedHash := range metadata.ChunkHashes {
		chunkName := fmt.Sprintf("chunk_%d.chunk", index)
		localChunkPath := filepath.Join(app.chunkStoragePath, fileName, chunkName)
		stagedChunkPath := filepath.Join(stagedFileDir, chunkName)

		if existingData, readErr := os.ReadFile(localChunkPath); readErr == nil {
			sum := sha256.Sum256(existingData)
			if hex.EncodeToString(sum[:]) == expectedHash {
				if writeErr := os.WriteFile(stagedChunkPath, existingData, 0644); writeErr == nil {
					continue
				}
			}
		}

		chunkData, fetchErr := network.FetchChunkFromAddress(requestContext, address, expectedHash, chunkClient)
		if fetchErr != nil && app.deviceRegistry != nil && app.deviceRegistry.Status().Registered {
			if peerData, _, peerErr := app.deviceRegistry.RetrieveChunk(requestContext, expectedHash); peerErr == nil {
				chunkData = peerData
				fetchErr = nil
			}
		}
		if fetchErr != nil {
			return nil, fmt.Errorf("pull chunk %d: %w", index, fetchErr)
		}
		if writeErr := os.WriteFile(stagedChunkPath, chunkData, 0644); writeErr != nil {
			return nil, writeErr
		}
		result.ChunksSent++
		result.ChunkBytesSent += int64(len(chunkData))
	}

	applyErr := app.applyIncomingTransfer(metadata, stagingRoot)
	if applyErr != nil {
		var syncConflictError *network.SyncConflictError
		var keepLocalError *network.KeepLocalError
		if errors.As(applyErr, &syncConflictError) {
			result.ConflictFile = syncConflictError.ConflictFile
			return result, nil
		}
		if errors.As(applyErr, &keepLocalError) {
			result.KeepLocal = true
			return result, nil
		}
		return nil, fmt.Errorf("apply pulled file: %w", applyErr)
	}

	// Cache pulled chunks for future incremental comparisons and index them.
	localChunkDir := filepath.Join(app.chunkStoragePath, fileName)
	if err := os.MkdirAll(localChunkDir, 0755); err == nil {
		for index, expectedHash := range metadata.ChunkHashes {
			chunkName := fmt.Sprintf("chunk_%d.chunk", index)
			if data, err := os.ReadFile(filepath.Join(stagedFileDir, chunkName)); err == nil {
				localPath := filepath.Join(localChunkDir, chunkName)
				if writeErr := os.WriteFile(localPath, data, 0644); writeErr == nil && app.chunkIndex != nil {
					app.chunkIndex.Add(expectedHash, localPath)
				}
			}
		}
		app.scheduleLocalChunkInventoryReport()
	}
	result.BaseUpdated = true
	return result, nil
}

// GetFileSyncStatuses returns a per-file sync state ("Synced", "Conflict", "Modified", or "Local only").
func (app *App) GetFileSyncStatuses() map[string]string {
	statuses := make(map[string]string)
	if app.db == nil {
		return statuses
	}
	openConflicts := make(map[string]bool)
	if conflictRows, err := app.db.Query("SELECT DISTINCT file_id FROM conflicts WHERE status = 'open'"); err == nil {
		for conflictRows.Next() {
			var fileID string
			if conflictRows.Scan(&fileID) == nil {
				openConflicts[fileID] = true
			}
		}
		_ = conflictRows.Err()
		_ = conflictRows.Close()
	}
	rows, err := app.db.Query(`SELECT f.id, COALESCE(f.last_synced_hash, ''), COALESCE(v.content_hash, '')
		FROM files f
		LEFT JOIN file_versions v ON v.file_id = f.id AND v.version_number = f.current_version`)
	if err != nil {
		return statuses
	}
	defer rows.Close()
	for rows.Next() {
		var fileID, baseHash, contentHash string
		if rows.Scan(&fileID, &baseHash, &contentHash) != nil {
			continue
		}
		switch {
		case openConflicts[fileID]:
			statuses[fileID] = "Conflict"
		case baseHash == "":
			statuses[fileID] = "Local only"
		case baseHash == contentHash && contentHash != "":
			statuses[fileID] = "Synced"
		default:
			statuses[fileID] = "Modified"
		}
	}
	if rows.Err() != nil {
		return statuses
	}
	return statuses
}
