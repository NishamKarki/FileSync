package main

import (
	"FileSyncWails/network"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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

	// Initialize FileWatcher function and start monitoring "Synced Files" folder at startup
	a.FileWatcher(a.syncFolderPath)

	// Start the local HTTP Server
	//Rabindra Neupane
	// Reconstruction implementation: Nisham Karki
	// Network integration: Rabindra Neupane
	go network.StartServer("8080", a.ReconstructFile)
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

// Rabindra Neupane
// DiscoverDevices discovers devices on the local network
func (a *App) DiscoverDevices() []network.Device {
	return network.DiscoverDevices()
}

// /Rabindra Neupane
// SendFile sends a file to another FileSync device on the network
func (a *App) SendFile(address string, fileName string) error {
	// Create the path of the file that will be sent
	filePath := filepath.Join(a.syncFolderPath, fileName)

	return network.SendFile(address, filePath)
}

// /Rabindra Neupane
// SendFirstChunk send the first chunk of a file to another FileSync device on the network
// This is currently being used to test chunk based network transfer
func (a *App) SendFirstChunk(address string, fileName string) error {
	// Create path of the selectd file
	filePath := filepath.Join(a.syncFolderPath, fileName)

	// Generate file chunks to test chunk transfer
	chunks, chunkError := a.ChunkFile(filePath)
	if chunkError != nil {
		return fmt.Errorf("failed to chunk file %s: %w", filePath, chunkError)
	}

	// Check if any chunks were created for the file
	// Returns output if the selected file does not create any chunks
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks created for file: %s", filePath)
	}

	// Select the first chunk to test for network transfer
	firstChunk := chunks[0]

	fmt.Printf("Sending chunk %d of %s to %s\n", firstChunk.Index, fileName, address)

	// Send the selected chunk to the receving end of FileSync devices
	return network.SendChunk(address, fileName, firstChunk.Index, len(chunks), firstChunk.Hash, firstChunk.Data)
}

// Rabindra Neupane
// SendAllChunks sends all chunks of a file to another FileSync device on the network
func (a *App) SendAllChunks(address string, fileName string) error {
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
