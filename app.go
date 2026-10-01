package main

import (
	"FileSyncWails/network"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// App struct
type App struct {
	ctx              context.Context
	syncFolderPath   string
	fileWatcher      *fsnotify.Watcher
	chunkStoragePath string
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

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

	// Create "File Chunks" folder, if it doesn't already exist
	os.MkdirAll(a.syncFolderPath, 0755)

	// Initialize FileWatcher function at program startup
	a.FileWatcher(a.syncFolderPath)

	// Create path to save chunks inside "File Chunks" folder
	a.chunkStoragePath = filepath.Join(
		getProjectDirectory,
		"File Chunks",
	)

	os.MkdirAll(a.chunkStoragePath, 0755)

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
