///// Author: Nisham Karki

package main

import (
	"fmt"
	"path/filepath"

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

	// Check for Files create event
	if fileWatcherEvent.Op == fsnotify.Create {
		fmt.Println("CREATE:", fileName)

		// Return the file created event
		runtime.EventsEmit(
			app.ctx, "file-change", "Created: "+fileName,
		)
	}
	// Check for Files Write (modified) event
	if fileWatcherEvent.Op == fsnotify.Write {
		fmt.Println("WRITE:", fileName)
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
