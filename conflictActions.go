// Author: Santosh Tripathee

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Works out where the two versions in a conflict are on disk:
//   - localPath: the user's file in Synced Files
//   - copyPath: the other device's version, saved in Synced Files Conflict
//
// It also checks that the database record points only inside those two
// folders, so a damaged record can never make FileSync touch other files.
func (app *App) conflictFilePaths(conflict ConflictRecord) (string, string, error) {

	// File IDs are plain file names in Synced Files, with no folders in them
	if conflict.FileID == "" || filepath.Base(conflict.FileID) != conflict.FileID {
		return "", "", fmt.Errorf("conflict %d has an invalid file name", conflict.ID)
	}
	localPath := filepath.Join(app.syncFolderPath, conflict.FileID)

	// Conflict copies are kept in Synced Files Conflict, next to Synced Files
	projectFolder := filepath.Dir(app.syncFolderPath)
	conflictFolder := filepath.Join(projectFolder, "Synced Files Conflict")
	copyPath := filepath.Join(projectFolder, conflict.ConflictCopy)
	if filepath.Dir(copyPath) != conflictFolder {
		return "", "", fmt.Errorf("conflict %d has an invalid copy location", conflict.ID)
	}

	return localPath, copyPath, nil
}

// Checks that the other device's saved version still exists and has not
// changed, by comparing its SHA-256 with the hash recorded for the conflict.
func checkSavedCopy(copyPath string, expectedHash string) error {
	// Hash the archived incoming copy from its current disk contents.
	savedCopyHash, copyHashingError := HashFileOnDisk(copyPath)
	// Return when the saved copy cannot be read.
	if copyHashingError != nil {
		return copyHashingError
	}

	// HashFileOnDisk returns "" when the file doesn't exist
	if savedCopyHash != expectedHash {
		return fmt.Errorf("the saved copy of the other device's version is missing or damaged: %s", filepath.Base(copyPath))
	}
	return nil
}

// Keep both: the other device's version is copied into Synced Files under a
// new name, such as "notes (conflict copy).txt", so it becomes a normal file
// that syncs like any other. The user's file is not changed, and the archived
// copy in Synced Files Conflict stays where it is.
// Returns the name of the new file.
func (app *App) keepBothVersions(conflict ConflictRecord) (string, error) {
	// Resolve and validate the archived incoming copy path.
	_, copyPath, pathError := app.conflictFilePaths(conflict)
	if pathError != nil {
		return "", pathError
	}
	// Verify that the archived copy still matches the recorded incoming hash.
	if savedCopyError := checkSavedCopy(copyPath, conflict.IncomingHash); savedCopyError != nil {
		return "", savedCopyError
	}

	// "notes.txt" becomes "notes" + ".txt"
	extension := filepath.Ext(conflict.FileID)
	nameWithoutExtension := strings.TrimSuffix(conflict.FileID, extension)

	// Try "notes (conflict copy).txt", then "notes (conflict copy 2).txt", and
	// so on, so an existing file is never overwritten
	for copyNumber := 1; copyNumber <= 100; copyNumber++ {
		// Build the first label or a numbered label for later attempts.
		copyLabel := "conflict copy"
		if copyNumber > 1 {
			copyLabel = fmt.Sprintf("conflict copy %d", copyNumber)
		}
		newFileName := fmt.Sprintf("%s (%s)%s", nameWithoutExtension, copyLabel, extension)
		// Place the new independent copy inside Synced Files.
		newFilePath := filepath.Join(app.syncFolderPath, newFileName)

		// copyRemoteBackup (Rabindra's) only creates a file that doesn't exist
		// yet. If the name is taken, it fails with an "already exists" error and
		// we try the next number.
		copyingError := copyRemoteBackup(copyPath, newFilePath)
		// Return the first file name that was created successfully.
		if copyingError == nil {
			return newFileName, nil
		}
		// Stop on errors other than an already-used file name.
		if !os.IsExist(copyingError) {
			return "", copyingError
		}
	}
	// Stop after the safety limit prevents an endless naming loop.
	return "", fmt.Errorf("too many conflict copies of %s already exist in Synced Files", conflict.FileID)
}

// Use incoming: the other device's version replaces the user's file.
// Nothing is lost: the user's current version is first saved in
// Synced Files Conflict as, for example, "notes.replaced-<hash>.txt".
// Returns the name of that backup.
func (app *App) useIncomingVersion(conflict ConflictRecord) (string, error) {
	// Resolve and validate the local and archived incoming paths.
	localPath, copyPath, pathError := app.conflictFilePaths(conflict)
	if pathError != nil {
		return "", pathError
	}
	// Verify that the archived incoming version is still intact.
	if savedCopyError := checkSavedCopy(copyPath, conflict.IncomingHash); savedCopyError != nil {
		return "", savedCopyError
	}

	// The user's file must be exactly as it was when the conflict happened.
	// If they edited it since, this decision would be about an old version.
	if unchangedError := checkLocalFileUnchanged(localPath, conflict); unchangedError != nil {
		return "", unchangedError
	}

	// 1. Back up the user's version in Synced Files Conflict.
	// The hash in the name means the same version is only backed up once.
	conflictFolder := filepath.Dir(copyPath)
	extension := filepath.Ext(conflict.FileID)
	backupName := strings.TrimSuffix(conflict.FileID, extension) + ".replaced-" + conflict.LocalHash + extension
	backupPath := filepath.Join(conflictFolder, backupName)

	// Save the current local version before replacing it.
	backupError := copyRemoteBackup(localPath, backupPath)
	// Accept an existing verified backup from an earlier attempt.
	if backupError != nil {
		// Return unexpected backup failures immediately.
		if !os.IsExist(backupError) {
			return "", backupError
		}
		// Backed up before (for example, an earlier attempt). Make sure it's intact.
		if existingBackupError := checkSavedCopy(backupPath, conflict.LocalHash); existingBackupError != nil {
			return "", existingBackupError
		}
	}

	// 2. Copy the other device's version to a temporary file outside Synced
	// Files, then move it into place in one step. The file watcher and other
	// programs never see a half-written file, and the archived copy stays.
	temporaryPath := filepath.Join(conflictFolder, "incoming-"+conflict.IncomingHash+".tmp")
	// Remove a temporary file left by an earlier failed attempt.
	os.Remove(temporaryPath) // a leftover from an earlier failed attempt, if any
	// Copy the verified incoming version into temporary replacement storage.
	if temporaryCopyError := copyRemoteBackup(copyPath, temporaryPath); temporaryCopyError != nil {
		return "", temporaryCopyError
	}

	// Check once more right before replacing, in case the user saved the
	// file while the copies were being made
	if unchangedError := checkLocalFileUnchanged(localPath, conflict); unchangedError != nil {
		// Remove temporary data when the local file changed during preparation.
		os.Remove(temporaryPath)
		return "", unchangedError
	}
	// Atomically replace the local file with the verified incoming version.
	if replaceError := replaceVerifiedFile(temporaryPath, localPath); replaceError != nil {
		// Remove temporary data when replacement fails.
		os.Remove(temporaryPath)
		return "", replaceError
	}

	// Return the name of the preserved previous local version.
	return backupName, nil
}

// Checks that the user's file still has the hash it had when the conflict
// was detected.
func checkLocalFileUnchanged(localPath string, conflict ConflictRecord) error {
	// Hash the user's current file before applying a stored conflict decision.
	currentLocalHash, localHashingError := HashFileOnDisk(localPath)
	// Return when the local file cannot be read.
	if localHashingError != nil {
		return localHashingError
	}
	// Reject the decision when the user edited the file after detection.
	if currentLocalHash != conflict.LocalHash {
		return fmt.Errorf("%s changed after the conflict was detected. Sync it again so FileSync can decide with the latest version", conflict.FileID)
	}
	// Return success when the local version still matches the conflict record.
	return nil
}
