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
	ctx            context.Context
	syncFolderPath string
	fileWatcher    *fsnotify.Watcher
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
		return
	}

	// Save files inside a dedicated synced files folder
	a.syncFolderPath = filepath.Join(
		getProjectDirectory,
		"Synced Files",
	)

	// Checking where files is being saved unless lost
	fmt.Println("Sync folder path:", a.syncFolderPath)

	// Create the folder if it doesn't exist, create a "Synced Files" folder
	syncFolderCreationError := os.MkdirAll(
		a.syncFolderPath,
		0755,
	)

	// Check error during sync folder creation
	if syncFolderCreationError != nil {
		return
	}

	go network.StartServer("8080")
}

// PingDevice pings a device at the specified address
func (a *App) PingDevice(address string) (network.PingResponse, error) {
	response, err := network.PingDevice(address)
	// Check for error during ping
	if err != nil {
		return network.PingResponse{}, err
	}

	return *response, nil
}

// GetLocalIP retrieves the local IP address of the machine
func (a *App) GetLocalIP() (string, error) {
	return network.GetLocalIP()
}

// DiscoverDevices discovers devices on the local network
func (a *App) DiscoverDevices() []string {
	return network.DiscoverDevices()
}
