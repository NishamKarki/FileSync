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

//Rabindra Neupane
// Multi-file selection extends Nisham Karki's picker and copy workflow.
type FileImportResult struct {
	Names  []string `json:"names"`
	Errors []string `json:"errors"`
}

//Rabindra Neupane
func (app *App) AddFiles() (*FileImportResult, error) {
	paths, err := runtime.OpenMultipleFilesDialog(app.ctx, runtime.OpenDialogOptions{
		Title: "Select files to add to FileSync",
	})
	if err != nil {
		return nil, err
	}
	result := &FileImportResult{Names: make([]string, 0), Errors: make([]string, 0)}
	seen := make(map[string]bool)
	for _, path := range paths {
		name := app.addSelectedFile(path)
		if name == "" {
			result.Errors = append(result.Errors, "Could not import "+filepath.Base(path))
		} else if !seen[name] {
			result.Names = append(result.Names, name)
			seen[name] = true
		}
	}
	return result, nil
}

//Rabindra Neupane
// File picker/copy implementation: Nisham Karki.
// Existing-file selection and testable path handling: Rabindra Neupane.
func (app *App) addSelectedFile(selectFilePath string) string {

	// Get selected file name
	fileName := filepath.Base(selectFilePath)

	// Create destination path for files to sync
	destinationFilePath := filepath.Join(
		app.syncFolderPath,
		fileName,
	)

	// Check whether file already exists in the Synced files
	destinationFilePath, ifFileAlreadyExists :=
		GetAvailableFilePath(
			selectFilePath,
			app.syncFolderPath,
		)

	//Rabindra Neupane
	// Select the existing synced copy without copying or overwriting it.
	if ifFileAlreadyExists {

		fmt.Println(
			filepath.Base(destinationFilePath),
			"selected from Synced Files",
		)

		return filepath.Base(destinationFilePath)
	}

	// Open original file
	sourceFile, sourceFileOpenError :=
		os.Open(selectFilePath)

	if sourceFileOpenError != nil {
		return ""
	}

	// Close the original files
	defer sourceFile.Close()

	// Create copied file
	destinationFile, destinationFileCreationError :=
		os.Create(destinationFilePath)

	if destinationFileCreationError != nil {
		return ""
	}

	defer destinationFile.Close()

	// Copy contents from original into FileSync
	//Rabindra Neupane
	// Do not select a partial copy if importing one of the files fails.
	_, copyError := io.Copy(destinationFile, sourceFile)
	closeError := destinationFile.Close()
	if copyError != nil || closeError != nil {
		os.Remove(destinationFilePath)
		return ""
	}

	fmt.Println(
		filepath.Base(destinationFilePath),
		"copied successfully",
	)

	return filepath.Base(destinationFilePath)
}

//Rabindra Neupane
// SyncedFile describes a working file, without claiming it matches a remote peer.
type SyncedFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

//Rabindra Neupane
// ListSyncedFiles reads the working folder directly; no metadata schema changes.
// The current sync protocol supports files in this folder, not nested folders.
func (app *App) ListSyncedFiles() ([]SyncedFile, error) {
	if app.syncFolderPath == "" {
		return nil, fmt.Errorf("Synced Files is not ready")
	}
	entries, err := os.ReadDir(app.syncFolderPath)
	if err != nil {
		return nil, fmt.Errorf("read Synced Files: %w", err)
	}
	files := make([]SyncedFile, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read file details: %w", err)
		}
		files = append(files, SyncedFile{Name: entry.Name(), Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339Nano)})
	}
	return files, nil
}
