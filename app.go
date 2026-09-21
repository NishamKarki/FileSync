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

	// Checking where the files are being saved in case they are lost
	fmt.Println("Sync folder path:", a.syncFolderPath)

	// Create the folder if it doesn't exist, create a "Synced Files" folder
	os.MkdirAll(a.syncFolderPath, 0755)

	a.FileWatcher(a.syncFolderPath)

	go network.StartServer("8080")
}

func (a *App) PingDevice(address string) (network.PingResponse, error) {
	response, err := network.PingDevice(address)

	if err != nil {
		return network.PingResponse{}, err
	}

	return *response, nil
}
