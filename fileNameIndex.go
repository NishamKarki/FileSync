// Author: Nisham Karki

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func GetAvailableFilePath(
	selectedFilePath string,
	syncFolderPath string,
) (string, bool) {

	// Get the original file name
	fileName := filepath.Base(selectedFilePath)

	// Get file extension
	fileExtension := filepath.Ext(fileName)

	// Get file name without extension
	fileNameWithoutExtension := strings.TrimSuffix(
		fileName,
		fileExtension,
	)

	// Start with the original file name
	fileIndex := 0

	for {

		var newFileName string

		// First check original name
		if fileIndex == 0 {
			newFileName = fileName
		} else {

			// Give index to file names
			// report (1).docx
			// report (2).docx
			newFileName = fmt.Sprintf(
				"%s (%d)%s",
				fileNameWithoutExtension,
				fileIndex,
				fileExtension,
			)
		}

		// Create full destination path
		destinationFilePath := filepath.Join(
			syncFolderPath,
			newFileName,
		)

		// Check if this name already exists
		_, destinationCheckError := os.Stat(
			destinationFilePath,
		)

		// File does not exist, so this name is available to use
		if os.IsNotExist(destinationCheckError) {
			return destinationFilePath, false
		}

		// File exists, so compare contents of the files
		sameFileOpened := CompareFiles(
			selectedFilePath,
			destinationFilePath,
		)

		// Same contents means that it is already synced
		if sameFileOpened {
			return destinationFilePath, true
		}

		fileIndex++
	}
}
