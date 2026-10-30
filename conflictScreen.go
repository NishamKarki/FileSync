// Author: Santosh Tripathee

package main

import (
	"fmt"
)

// Called by the screen (the Conflicts button) to get the conflicts that
// still need the user's decision. Wails makes every exported App method
// available to the frontend automatically.
func (app *App) GetConflicts() ([]ConflictRecord, error) {
	if app.db == nil {
		return nil, fmt.Errorf("metadata database is unavailable")
	}
	return ListOpenConflicts(app.db)
}

// The three choices the conflict window can send
const (
	KeepMineChoice    = "keep-mine"
	UseIncomingChoice = "use-incoming"
	KeepBothChoice    = "keep-both"
)

// What the conflict window shows after the user's choice is carried out
type ConflictResolution struct {
	FileID  string `json:"fileId"`
	Message string `json:"message"`
}

// Called by the screen when the user clicks Keep mine, Use incoming or
// Keep both. It carries out the choice, then saves the decision and moves
// the file's base (MarkConflictResolved).
//
// Files are changed first and the decision is saved after, so if a file
// step fails, the conflict stays open and the user can choose again.
func (app *App) ResolveConflict(conflictID int64, choice string) (ConflictResolution, error) {

	var resolution ConflictResolution
	if app.db == nil {
		return resolution, fmt.Errorf("metadata database is unavailable")
	}

	// The same lock Rabindra's receive code and Nisham's watcher use, so a
	// file arriving from another device can't change these files while the
	// choice is being carried out
	app.fileProcessingMutex.Lock()
	defer app.fileProcessingMutex.Unlock()

	conflict, loadError := GetOpenConflict(app.db, conflictID)
	if loadError != nil {
		return resolution, loadError
	}

	var resultMessage string
	switch choice {

	case KeepMineChoice:
		// No files change: the user's file stays, and the other device's
		// version stays archived in Synced Files Conflict
		resultMessage = fmt.Sprintf("Kept your version of %s. The other device's version stays archived in Synced Files Conflict.", conflict.FileID)

	case UseIncomingChoice:
		backupName, useIncomingError := app.useIncomingVersion(conflict)
		if useIncomingError != nil {
			return resolution, useIncomingError
		}
		resultMessage = fmt.Sprintf("%s now has the other device's version. Your previous version was saved as %s in Synced Files Conflict.", conflict.FileID, backupName)

	case KeepBothChoice:
		newFileName, keepBothError := app.keepBothVersions(conflict)
		if keepBothError != nil {
			return resolution, keepBothError
		}
		resultMessage = fmt.Sprintf("Kept both: your version stays as %s, and the other device's version is now %s.", conflict.FileID, newFileName)

	default:
		return resolution, fmt.Errorf("unknown choice %q", choice)
	}

	// Save the decision and move the base, together
	if resolveError := MarkConflictResolved(app.db, conflict, choice); resolveError != nil {
		return resolution, resolveError
	}

	resolution.FileID = conflict.FileID
	resolution.Message = resultMessage + fmt.Sprintf(" Sync %s to the other device so it gets your decision.", conflict.FileID)
	return resolution, nil
}
