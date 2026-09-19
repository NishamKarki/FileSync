/////// AUTHOR : Nisham Karki

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (app *App) AddFile() string {

	// Open file picker widow
	selectFilePath, filePickingError := runtime.OpenFileDialog(
		app.ctx,
		runtime.OpenDialogOptions{
			Title: "Select a file to add to FileSync",
		},
	)

	// if there is error while picking file for sync
	if filePickingError != nil {
		return ""
	}

	// If user cancels, return nothing
	if selectFilePath == "" {
		return ""
	}

	// Get selected file name
	fileName := filepath.Base(selectFilePath)

	// Create destination path for files to sync
	destinationFilePath := filepath.Join(
		app.syncFolderPath,
		fileName,
	)

	// Check whether file already exists in the Synced files
	destinationFileInfo, destinationCheckError :=
		os.Stat(destinationFilePath)

	// Return message if a files already exists in the folder
	if destinationCheckError == nil {
		fmt.Println(
			destinationFileInfo.Name(),
			"already exists in Synced Files",
		)

		return ""
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
	io.Copy(destinationFile, sourceFile)

	fmt.Println(
		fileName,
		"copied successfully",
	)

	return fileName
}
