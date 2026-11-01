/////// AUTHOR : Nisham Karki

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Rabindra Neupane
// Multi-file selection extends Nisham Karki's picker and copy workflow.
type FileImportResult struct {
	Names  []string `json:"names"`
	Errors []string `json:"errors"`
}

// Rabindra Neupane
// AddFiles opens multi-file selection and imports each chosen file into Synced Files.
func (app *App) AddFiles() (*FileImportResult, error) {
	// Open the system dialog and allow more than one selected file.
	selectedFilePaths, fileDialogError := runtime.OpenMultipleFilesDialog(app.ctx, runtime.OpenDialogOptions{
		Title: "Select files to add to FileSync",
	})
	if fileDialogError != nil {
		return nil, fileDialogError
	}
	// Create separate result lists for imported files and failed imports.
	result := &FileImportResult{Names: make([]string, 0), Errors: make([]string, 0)}
	// Track imported names so the result does not contain duplicates.
	seen := make(map[string]bool)
	// Import every file selected in the system dialog.
	for _, selectedFilePath := range selectedFilePaths {
		// Copy or select this file inside Synced Files.
		name := app.addSelectedFile(selectedFilePath)
		// Record a failure or add the successfully imported name once.
		if name == "" {
			result.Errors = append(result.Errors, "Could not import "+filepath.Base(selectedFilePath))
		} else if !seen[name] {
			result.Names = append(result.Names, name)
			seen[name] = true
		}
	}
	// Return all successful imports and individual failures.
	return result, nil
}

// Rabindra Neupane
// addSelectedFile copies one selected file or selects its existing Synced Files copy.
// File copy: Nisham Karki. Existing-file handling: Rabindra Neupane.
func (app *App) addSelectedFile(selectFilePath string) string {

	// Read the selected file name without its outside folder path.
	fileName := filepath.Base(selectFilePath)

	// Create the default destination inside Synced Files.
	destinationFilePath := filepath.Join(
		app.syncFolderPath,
		fileName,
	)

	// Check for an existing copy or find an available destination name.
	destinationFilePath, ifFileAlreadyExists :=
		GetAvailableFilePath(
			selectFilePath,
			app.syncFolderPath,
		)

	// Return the existing synced copy without overwriting it.
	if ifFileAlreadyExists {

		fmt.Println(
			filepath.Base(destinationFilePath),
			"selected from Synced Files",
		)

		return filepath.Base(destinationFilePath)
	}

	// Open the outside source file for reading.
	sourceFile, sourceFileOpenError :=
		os.Open(selectFilePath)

	if sourceFileOpenError != nil {
		return ""
	}

	// Close the source file when copying finishes.
	defer sourceFile.Close()

	// Create the new destination file.
	destinationFile, destinationFileCreationError :=
		os.Create(destinationFilePath)

	if destinationFileCreationError != nil {
		return ""
	}

	// Close the destination if the copy exits before the explicit close below.
	defer destinationFile.Close()

	// Copy all bytes and remove a partial destination when copying fails.
	_, fileCopyingError := io.Copy(destinationFile, sourceFile)
	destinationFileClosingError := destinationFile.Close()
	if fileCopyingError != nil || destinationFileClosingError != nil {
		// Remove incomplete output so it is not shown as a synced file.
		os.Remove(destinationFilePath)
		return ""
	}

	fmt.Println(
		filepath.Base(destinationFilePath),
		"copied successfully",
	)

	// Return the imported file name used by the frontend selection list.
	return filepath.Base(destinationFilePath)
}

// Rabindra Neupane
// SyncedFile describes a working file, without claiming it matches a remote peer.
type SyncedFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// Rabindra Neupane
// ListSyncedFiles reads the working folder directly; no metadata schema changes.
// The current sync protocol supports files in this folder, not nested folders.
func (app *App) ListSyncedFiles() ([]SyncedFile, error) {
	// Check that application startup prepared the working folder.
	if app.syncFolderPath == "" {
		return nil, fmt.Errorf("Synced Files is not ready")
	}
	// Read the current entries directly from Synced Files.
	directoryEntries, directoryReadingError := os.ReadDir(app.syncFolderPath)
	if directoryReadingError != nil {
		return nil, fmt.Errorf("read Synced Files: %w", directoryReadingError)
	}
	// Create the file list with enough capacity for all directory entries.
	files := make([]SyncedFile, 0, len(directoryEntries))
	// Inspect every entry and keep only normal files.
	for _, entry := range directoryEntries {
		// Skip folders and other non-file entries.
		if !entry.Type().IsRegular() {
			continue
		}
		// Read file size and modification time for the frontend table.
		fileInformation, informationReadingError := entry.Info()
		// Ignore a file that disappeared during the directory scan.
		if os.IsNotExist(informationReadingError) {
			continue
		}
		// Stop when file details fail for another reason.
		if informationReadingError != nil {
			return nil, fmt.Errorf("read file details: %w", informationReadingError)
		}
		// Add the file using a stable UTC modification timestamp.
		files = append(files, SyncedFile{Name: entry.Name(), Size: fileInformation.Size(), Modified: fileInformation.ModTime().UTC().Format(time.RFC3339Nano)})
	}
	// Return the complete current Synced Files list.
	return files, nil
}
