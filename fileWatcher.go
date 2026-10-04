///// Author: Nisham Karki

package main

import (
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

	// // Ignore .~tmp files
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
		// Return the file deleted event
		runtime.EventsEmit(
			app.ctx, "file-change", "Removed: "+fileName,
		)
	}

}

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

			chunks := app.ChunkFile(filePath)

			app.SaveChunks(
				filePath,
				chunks,
			)
		},
	)

	app.timerMutex.Unlock()
}
