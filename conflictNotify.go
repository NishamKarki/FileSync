// Author: Santosh Tripathee

package main

import (
	"FileSyncWails/network"
	"errors"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// recordDetectedConflict stores a preserved incoming conflict and notifies the UI.
func (app *App) recordDetectedConflict(metadata network.FileMetadata, localHash string, conflictPreservationResult error) {
	// Check that the apply result is a preserved synchronization conflict.
	var synchronizationConflictError *network.SyncConflictError
	if !errors.As(conflictPreservationResult, &synchronizationConflictError) {
		return
	}

	// Save the conflict details used by the resolution screen.
	conflictRecordingError := RecordConflict(app.db, ConflictRecord{
		FileID:         metadata.FileID,
		LocalHash:      localHash,
		IncomingHash:   metadata.FileHash,
		BaseHash:       metadata.BaseHash,
		SenderDeviceID: metadata.DeviceID,
		ConflictCopy:   synchronizationConflictError.ConflictFile,
	})
	// Log a storage failure without hiding the already preserved file copy.
	if conflictRecordingError != nil {
		fmt.Println("Failed to record conflict:", conflictRecordingError)
		return
	}

	// Tests do not have an active Wails event context.
	if app.ctx != nil && app.ctx.Value("events") != nil {
		runtime.EventsEmit(app.ctx, "conflict-detected", metadata.FileName)
	}
}
