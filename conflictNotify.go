// Author: Santosh Tripathee

package main

import (
	"FileSyncWails/network"
	"errors"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Called when DetectConflict decides "conflict" for a file that arrived from
// another device. Saves the conflict and tells the screen straight away.
// localHash is the receiver's file as it was when the decision was made.
func (app *App) recordDetectedConflict(metadata network.FileMetadata, localHash string, preserveResult error) {

	// Rabindra's preserveIncomingConflict returns a SyncConflictError that says
	// where the incoming copy was saved. Any other result means the copy was
	// not saved, so there is no conflict to show yet.
	var conflictError *network.SyncConflictError
	if !errors.As(preserveResult, &conflictError) {
		return
	}

	recordError := RecordConflict(app.db, ConflictRecord{
		FileID:         metadata.FileID,
		LocalHash:      localHash,
		IncomingHash:   metadata.FileHash,
		BaseHash:       metadata.BaseHash,
		SenderDeviceID: metadata.DeviceID,
		ConflictCopy:   conflictError.ConflictFile,
	})
	if recordError != nil {
		// The transfer result stays the same; only the notification is lost
		fmt.Println("Failed to record conflict:", recordError)
		return
	}

	// Tell the screen, the same way fileWatcher.go sends "file-change".
	// Only the running app has a Wails screen. Tests don't, and Wails stops
	// the whole program if it is asked to send an event without one.
	if app.ctx != nil && app.ctx.Value("events") != nil {
		runtime.EventsEmit(app.ctx, "conflict-detected", metadata.FileName)
	}
}
