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

	// Initialize a file Watcher system
	fileWatcher, fileWatcherCreationError := fsnotify.NewWatcher()

	// Checking for error
	if fileWatcherCreationError != nil {
		return fileWatcherCreationError
	}

	// Store the watcher in the application
	app.fileWatcher = fileWatcher

	// Synced Files folder path added to watcher for automatic change detection
	fileWatcher.Add(syncFolderPath)

	// Checking Synced Files folder path
	fmt.Println("Watching Foldre in: ", syncFolderPath)

	// Constantly listen for file system events
	// Checks for event changes
	go func() {
		for fileWatcherEvent := range fileWatcher.Events {
			app.HandleFileEvent(fileWatcherEvent)
		}
	}()

	return nil

}

// This funciton processes file system events detected by the watcher
func (app *App) HandleFileEvent(fileWatcherEvent fsnotify.Event) {
	// Get file name for most recently modified file
	fileName := filepath.Base(fileWatcherEvent.Name)

	// ~WRD1483.tmp, ~$ple situtaiton.docx, ~$arkiNI.docx
	// The above are the temporary files created by application during modification

	// Ignore files begining with "~"
	if strings.HasPrefix(fileName, "~") {
		fmt.Println("IGNORED temporary file:", fileName)
		return
	}

	// Ignore .~tmp files
	if strings.HasSuffix(strings.ToLower(fileName), ".~tmp") {
		fmt.Println("IGNORED temporary file:", fileName)
		return
	}

	// Ignore .TMP and .tmp files
	if strings.ToLower(filepath.Ext(fileName)) == ".tmp" {
		fmt.Println("IGNORED temporary file:", fileName)
		return
	}

	//Rabindra Neupane
	// Watcher implementation: Nisham Karki
	// Ignore only bytes already accepted as a remote version, not later user edits.
	app.fileProcessingMutex.Lock()
	defer app.fileProcessingMutex.Unlock()
	if app.matchesRemoteFile(fileWatcherEvent.Name) {
		return
	}

	// Check for Files create event
	//Rabindra Neupane
	if fileWatcherEvent.Op&fsnotify.Create != 0 {
		fmt.Println("CREATE:", fileName)

		// Delay processing breifly in case the file is still being created
		app.ScheduleFileProcessing(
			fileWatcherEvent.Name,
		)

		// Notify the frontend the file created event
		runtime.EventsEmit(
			app.ctx, "file-change", "Created: "+fileName,
		)
	}

	// Check for Files Write (modified) event
	// Copying a file into Synced File also trigger Write event
	//Rabindra Neupane
	if fileWatcherEvent.Op&fsnotify.Write != 0 {
		fmt.Println("WRITE:", fileName)

		// Delay processing until write events is completed
		app.ScheduleFileProcessing(
			fileWatcherEvent.Name,
		)

		// Notify the frontend the file modified event
		runtime.EventsEmit(
			app.ctx, "file-change", "Modified: "+fileName,
		)
	}

	// Check for Files rename event
	//Rabindra Neupane
	if fileWatcherEvent.Op&fsnotify.Rename != 0 {
		fmt.Println("RENAME:", fileName)

		// Notify the frontend the file rename event
		runtime.EventsEmit(
			app.ctx, "file-change", "Renamed: "+fileName,
		)
	}

	// Check for Files delete event
	//Rabindra Neupane
	if fileWatcherEvent.Op&fsnotify.Remove != 0 {
		fmt.Println("REMOVE:", fileName)

		// Convert the deteled/removed file path into relative file path
		// so that the matching file can be removed from the database
		relativePath, _ := filepath.Rel(app.syncFolderPath, fileWatcherEvent.Name)
		app.db.Exec("DELETE FROM files WHERE id = ?", relativePath)

		// Notify the frontend the file deleted event
		runtime.EventsEmit(
			app.ctx, "file-change", "Removed: "+fileName,
		)
	}

}

// This funciton waits briefly after the last change to a file before actually processing it
// Some application produce serveral create and/or write event during one save
// so this function timer prevent FileSync from creating multiple version of a single write/modification event
func (app *App) ScheduleFileProcessing(filePath string) {

	// Lock the timer before reading or changing it
	app.timerMutex.Lock()
	//Rabindra Neupane
	if app.fileTimers == nil {
		app.fileTimers = make(map[string]*time.Timer)
	}

	// Check if the file already has a timer
	existingTimer, timerExists := app.fileTimers[filePath]

	if timerExists {
		// File has changed so stop the old timer
		existingTimer.Stop()
	}

	// Start a new timer
	// Processing will start if no new event replaces this timer
	// during the next 700 miliseconds
	app.fileTimers[filePath] = time.AfterFunc(
		700*time.Millisecond,
		func() {

			// Remove this timer from the map
			app.timerMutex.Lock()

			delete(app.fileTimers, filePath)

			app.timerMutex.Unlock()

			//Rabindra Neupane
			// A pending timer may predate remote application. Check again here.
			app.fileProcessingMutex.Lock()
			defer app.fileProcessingMutex.Unlock()
			if app.matchesRemoteFile(filePath) {
				return
			}

			// Check if file still exists before processing it
			fileInfo, fileCheckerError := os.Stat(filePath)

			if fileCheckerError != nil {
				fmt.Println(
					"File no longer exists:",
					filepath.Base(filePath),
				)
				return
			}

			// Ignore directories event because FileSync should only process files.
			if fileInfo.IsDir() {
				return
			}

			fmt.Println(
				"PROCESSING:",
				filepath.Base(filePath),
			)

			//Rabindra Neupane
			// Watcher: Nisham Karki. Share serialized preparation with Sync Now;
			// Santosh's existing recorder deduplicates unchanged content.
			if _, _, err := app.prepareFileForSync(filepath.Base(filePath)); err != nil {
				fmt.Println("File processing error:", err)
			}
		},
	)

	// Allow other go routine to access the timer
	app.timerMutex.Unlock()
}
