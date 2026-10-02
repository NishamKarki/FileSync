///// Author: Nisham Karki

package main

import (
	"FileSyncWails/network"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Implement file Watcher, automatically Checks for any changes within Synced Files folder
func (app *App) FileWatcher(syncFolderPath string) error {

	// Initialize fileWatcher
	fileWatcher, fileWatcherCreationError := fsnotify.NewWatcher()

	// Checking for error
	if fileWatcherCreationError != nil {
		return fileWatcherCreationError
	}

	app.fileWatcher = fileWatcher

	// Synced Files folder path added for automatic change detection
	fileWatcher.Add(syncFolderPath)

	// Checking Synced Files folder path
	fmt.Println("Watching Foldre in: ", syncFolderPath)

	// Constant loop. Checks for event changes
	go func() {
		for fileWatcherEvent := range fileWatcher.Events {
			app.HandleFileEvent(fileWatcherEvent)
		}
	}()

	return nil

}

func (app *App) HandleFileEvent(fileWatcherEvent fsnotify.Event) {
	// Get file name for most recently modified file
	fileName := filepath.Base(fileWatcherEvent.Name)

	// ~WRD1483.tmp, ~$ple situtaiton.docx, ~$arkiNI.docx
	// The above are the temporary files created by application during modification
	// Ignore temporary files created
	if strings.HasPrefix(fileName, "~") {
		fmt.Println("IGNORED temporary file:", fileName)
		return
	}

	// Ignore .~tmp files
	if strings.HasSuffix(fileName, ".~tmp") {
		fmt.Println("IGNORED temporary file:", fileName)
		return
	}

	// Ignore .TMP and .tmp files
	if strings.ToLower(filepath.Ext(fileName)) == ".tmp" {
		fmt.Println("IGNORED temporary file:", fileName)
		return
	}

	// Check for Files create event
	if fileWatcherEvent.Op == fsnotify.Create {
		fmt.Println("CREATE:", fileName)

		app.ScheduleFileProcessing(
			fileWatcherEvent.Name,
		)

		// Return the file created event
		runtime.EventsEmit(
			app.ctx, "file-change", "Created: "+fileName,
		)
	}

	// Check for Files Write (modified) event
	if fileWatcherEvent.Op == fsnotify.Write {
		fmt.Println("WRITE:", fileName)

		// Run file chunking when a file is modified
		// Copying a file into Synced File also trigger Write event
		chunks, err := ChunkFile(fileWatcherEvent.Name)
		if err != nil {
			fmt.Println("Failed to chunk file:", err)
			return
		}

		// Save the chunks to the file storage
		app.SaveChunks(fileWatcherEvent.Name, chunks)

		// Schedule file processing after handling the write event
		app.ScheduleFileProcessing(
			fileWatcherEvent.Name,
		)

		// Return the file modified event
		runtime.EventsEmit(
			app.ctx, "file-change", "Modified: "+fileName,
		)
	}

	// Check for Files rename event
	if fileWatcherEvent.Op == fsnotify.Rename {
		fmt.Println("RENAME:", fileName)
		// Return the file rename event
		runtime.EventsEmit(
			app.ctx, "file-change", "Renamed: "+fileName,
		)
	}

	// Check for Files delete event
	if fileWatcherEvent.Op == fsnotify.Remove {
		fmt.Println("REMOVE:", fileName)

		relativePath, _ := filepath.Rel(app.syncFolderPath, fileWatcherEvent.Name)
		app.db.Exec("DELETE FROM files WHERE id = ?", relativePath)

		// Return the file deleted event
		runtime.EventsEmit(
			app.ctx, "file-change", "Removed: "+fileName,
		)
	}

}

// Waits briefly after the last change to a file before actually processing it,
// so rapid-fire write events (e.g. from a large copy) only trigger one version
// instead of several.
func (app *App) ScheduleFileProcessing(filePath string) {

	app.timerMutex.Lock()

	// Check if the file already has a timer
	existingTimer, timerExists := app.fileTimers[filePath]

	if timerExists {
		// File has changed so stop the old timer
		existingTimer.Stop()
	}

	// Create a new timer
	app.fileTimers[filePath] = time.AfterFunc(
		700*time.Millisecond,
		func() {

			// Remove this timer from the map
			app.timerMutex.Lock()
			delete(app.fileTimers, filePath)
			app.timerMutex.Unlock()

			// Check if file still exists
			fileInfo, fileCheckerError := os.Stat(filePath)

			if fileCheckerError != nil {
				fmt.Println(
					"File no longer exists:",
					filepath.Base(filePath),
				)
				return
			}

			// Ignore directories
			if fileInfo.IsDir() {
				return
			}

			fmt.Println(
				"PROCESSING:",
				filepath.Base(filePath),
			)

			deviceID, _ := network.GetLocalIP()
			recordError := RecordFileVersion(app.db, app.syncFolderPath, filePath, deviceID)
			if recordError != nil {
				fmt.Println("Failed to record file version:", recordError)
			}
		},
	)

	app.timerMutex.Unlock()
}
