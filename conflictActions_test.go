// Author: Santosh Tripathee

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Creates a temporary project folder with a real conflict in it:
//   - Synced Files/notes.txt holds this device's version ("hello B")
//   - Synced Files Conflict/notes.conflict-test.txt holds the other
//     device's version ("hello A"), as Rabindra's code would save it
//
// Returns the app, the conflict record, and the project folder.
func setupConflictFiles(test *testing.T) (*App, ConflictRecord, string) {
	test.Helper()

	projectFolder := test.TempDir()
	syncFolder := filepath.Join(projectFolder, "Synced Files")
	conflictFolder := filepath.Join(projectFolder, "Synced Files Conflict")
	for _, folder := range []string{syncFolder, conflictFolder} {
		if folderError := os.MkdirAll(folder, 0755); folderError != nil {
			test.Fatal(folderError)
		}
	}

	localPath := filepath.Join(syncFolder, "notes.txt")
	copyPath := filepath.Join(conflictFolder, "notes.conflict-test.txt")
	writeTestFile(test, localPath, "hello B")
	writeTestFile(test, copyPath, "hello A")

	localHash, _ := HashFileOnDisk(localPath)
	incomingHash, _ := HashFileOnDisk(copyPath)

	conflict := ConflictRecord{
		ID:           1,
		FileID:       "notes.txt",
		LocalHash:    localHash,
		IncomingHash: incomingHash,
		ConflictCopy: filepath.Join("Synced Files Conflict", "notes.conflict-test.txt"),
	}
	return &App{syncFolderPath: syncFolder}, conflict, projectFolder
}

func writeTestFile(test *testing.T, path string, content string) {
	test.Helper()
	if writeError := os.WriteFile(path, []byte(content), 0644); writeError != nil {
		test.Fatal(writeError)
	}
}

// Returns a file's text, or "<missing>" if it doesn't exist
func readTestFile(path string) string {
	content, readError := os.ReadFile(path)
	if readError != nil {
		return "<missing>"
	}
	return string(content)
}

// Keep both: the other version becomes a new file, nothing else changes,
// and an existing file is never overwritten.
func TestKeepBothVersions(test *testing.T) {
	app, conflict, projectFolder := setupConflictFiles(test)
	syncFolder := filepath.Join(projectFolder, "Synced Files")

	newFileName, keepBothError := app.keepBothVersions(conflict)
	if keepBothError != nil || newFileName != "notes (conflict copy).txt" {
		test.Fatalf("got %q (error: %v)", newFileName, keepBothError)
	}
	if text := readTestFile(filepath.Join(syncFolder, newFileName)); text != "hello A" {
		test.Fatalf("new file has %q, expected the other device's version", text)
	}
	if text := readTestFile(filepath.Join(syncFolder, "notes.txt")); text != "hello B" {
		test.Fatalf("the user's file changed to %q", text)
	}
	if text := readTestFile(filepath.Join(projectFolder, conflict.ConflictCopy)); text != "hello A" {
		test.Fatalf("the archived copy changed to %q", text)
	}

	// Doing it again uses the next number instead of overwriting
	secondFileName, secondError := app.keepBothVersions(conflict)
	if secondError != nil || secondFileName != "notes (conflict copy 2).txt" {
		test.Fatalf("second copy: got %q (error: %v)", secondFileName, secondError)
	}
}

// Use incoming: the other version replaces the user's file, and the user's
// version is backed up first, so nothing is lost.
func TestUseIncomingVersion(test *testing.T) {
	app, conflict, projectFolder := setupConflictFiles(test)
	syncFolder := filepath.Join(projectFolder, "Synced Files")
	conflictFolder := filepath.Join(projectFolder, "Synced Files Conflict")

	backupName, useIncomingError := app.useIncomingVersion(conflict)
	if useIncomingError != nil {
		test.Fatal(useIncomingError)
	}
	if text := readTestFile(filepath.Join(syncFolder, "notes.txt")); text != "hello A" {
		test.Fatalf("notes.txt has %q, expected the other device's version", text)
	}
	if text := readTestFile(filepath.Join(conflictFolder, backupName)); text != "hello B" {
		test.Fatalf("backup %s has %q, expected the user's old version", backupName, text)
	}
	if text := readTestFile(filepath.Join(projectFolder, conflict.ConflictCopy)); text != "hello A" {
		test.Fatalf("the archived copy changed to %q", text)
	}

	// No temporary file is left behind
	leftovers, _ := filepath.Glob(filepath.Join(conflictFolder, "*.tmp"))
	if len(leftovers) != 0 {
		test.Fatalf("temporary files left behind: %v", leftovers)
	}
}

// Use incoming is refused if the user edited their file after the conflict,
// and then nothing is changed or backed up.
func TestUseIncomingRefusesChangedFile(test *testing.T) {
	app, conflict, projectFolder := setupConflictFiles(test)
	localPath := filepath.Join(projectFolder, "Synced Files", "notes.txt")
	writeTestFile(test, localPath, "hello B, edited again")

	_, useIncomingError := app.useIncomingVersion(conflict)
	if useIncomingError == nil || !strings.Contains(useIncomingError.Error(), "changed after the conflict") {
		test.Fatalf("expected a 'changed after the conflict' error, got %v", useIncomingError)
	}
	if text := readTestFile(localPath); text != "hello B, edited again" {
		test.Fatalf("the user's new edit was changed to %q", text)
	}
	backups, _ := filepath.Glob(filepath.Join(projectFolder, "Synced Files Conflict", "*.replaced-*"))
	if len(backups) != 0 {
		test.Fatalf("a backup was made even though nothing was replaced: %v", backups)
	}
}

// A damaged saved copy, or a record pointing outside FileSync's folders,
// is refused before any file is created or changed.
func TestConflictActionsRefuseBadInput(test *testing.T) {
	app, conflict, projectFolder := setupConflictFiles(test)
	syncFolder := filepath.Join(projectFolder, "Synced Files")

	// The saved copy no longer matches its recorded hash
	writeTestFile(test, filepath.Join(projectFolder, conflict.ConflictCopy), "tampered")
	if _, keepBothError := app.keepBothVersions(conflict); keepBothError == nil {
		test.Fatal("keep both accepted a damaged copy")
	}
	if _, useIncomingError := app.useIncomingVersion(conflict); useIncomingError == nil {
		test.Fatal("use incoming accepted a damaged copy")
	}
	if text := readTestFile(filepath.Join(syncFolder, "notes.txt")); text != "hello B" {
		test.Fatalf("the user's file changed to %q", text)
	}
	if text := readTestFile(filepath.Join(syncFolder, "notes (conflict copy).txt")); text != "<missing>" {
		test.Fatal("a conflict copy was created from a damaged file")
	}

	// File names or copy locations that point outside FileSync's folders
	badFileName := conflict
	badFileName.FileID = filepath.Join("..", "outside.txt")
	if _, _, pathError := app.conflictFilePaths(badFileName); pathError == nil {
		test.Fatal("accepted a file name outside Synced Files")
	}
	badCopyLocation := conflict
	badCopyLocation.ConflictCopy = filepath.Join("Synced Files", "notes.txt")
	if _, _, pathError := app.conflictFilePaths(badCopyLocation); pathError == nil {
		test.Fatal("accepted a copy location outside Synced Files Conflict")
	}
}
