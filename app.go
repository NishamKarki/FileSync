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

// App struct
type App struct {
	ctx              context.Context
	syncFolderPath   string
	fileWatcher      *fsnotify.Watcher
	chunkStoragePath string
	db               *sql.DB
	fileTimers       map[string]*time.Timer
	timerMutex       sync.Mutex
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	a.fileTimers = make(map[string]*time.Timer)

	// Get the project's current directory
	getProjectDirectory, projectDirectoryError := os.Getwd()

	if projectDirectoryError != nil {
		fmt.Println("Error getting project directory: ", projectDirectoryError)
	}

	// Create to Save files inside a dedicated synced files folder
	a.syncFolderPath = filepath.Join(
		getProjectDirectory,
		"Synced Files",
	)

	// Checking where the files are being saved in case they are lost
	fmt.Println("Sync folder path:", a.syncFolderPath)

	// Create the folder if it doesn't exist, create a "Synced Files" folder
	// 0755: File Permission.
	// The file owner (Owner) can read, write, and execute,
	// while the group and others can read and execute but cannot write
	os.MkdirAll(a.syncFolderPath, 0755)

	// Create path to save chunks inside "File Chunks" folder
	a.chunkStoragePath = filepath.Join(
		getProjectDirectory,
		"File Chunks",
	)
	os.MkdirAll(a.chunkStoragePath, 0755)

	// Set up the metadata database
	db, dbError := InitDatabase(a.syncFolderPath)
	if dbError != nil {
		fmt.Println("Database setup failed:", dbError)
		return
	}
	a.db = db

	// Initialize FileWatcher function at program startup
	a.FileWatcher(a.syncFolderPath)

	go network.StartServer("8080")
}

// /Rabindra Neupane
// PingDevice pings a device at the specified address
func (a *App) PingDevice(address string) (network.PingResponse, error) {
	response, err := network.PingDevice(address)
	// Check for error during ping
	if err != nil {
		return network.PingResponse{}, err
	}

	return *response, nil
}

// /Rabindra Neupane
// GetLocalIP retrieves the local IP address of the machine
func (a *App) GetLocalIP() (string, error) {
	return network.GetLocalIP()
}

// /Rabindra Neupane
// DiscoverDevices discovers devices on the local network
func (a *App) DiscoverDevices() []network.Device {
	return network.DiscoverDevices()
}

// /Rabindra Neupane
// SendFile sends a file to another FileSync device on the network
func (a *App) SendFile(address string, fileName string) error {
	filePath := filepath.Join(a.syncFolderPath, fileName)

	return network.SendFile(address, filePath)
}

// /Rabindra Neupane
// SendChunk send the first chunk of a file to another FileSync device on the network
func (a *App) SendFirstChunk(address string, fileName string) error {
	filePath := filepath.Join(a.syncFolderPath, fileName)

	chunks, chunkError := a.ChunkFile(filePath)

	if chunkError != nil {
		return chunkError
	}

	// Check if any chunks were created for the file
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks created for file: %s", filePath)
	}

	firstChunk := chunks[0]

	fmt.Printf("Sending chunk %d of %s to %s\n", firstChunk.Index, fileName, address)
	return network.SendChunk(address, fileName, firstChunk.Index, firstChunk.Data)
}
