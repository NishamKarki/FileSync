package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// Create individual folder for each file
// Open a file to chunk. Save chunk in the folder
// Name the folder as the file's name

func (app *App) SaveChunks(filePath string, chunks []Chunk) {

	// Get file name
	fileName := filepath.Base(filePath)

	// Create a folder to keep the chunks for each file
	fileChunkFolder := filepath.Join(
		app.chunkStoragePath,
		fileName,
	)

	// Create chunk folder
	folderCreationError := os.MkdirAll(
		fileChunkFolder,
		0755,
	)

	if folderCreationError != nil {
		fmt.Println(
			"Chunk folder creation error:",
			folderCreationError,
		)
		return
	}

	for index := 0; index < len(chunks); index++ {

		chunk := chunks[index]

		// Create chunk file name
		chunkFileName := fmt.Sprintf(
			"chunk_%d.chunk",
			chunk.Index,
		)

		// Create chunk file path
		chunkFilePath := filepath.Join(
			fileChunkFolder,
			chunkFileName,
		)

		// Check if this chunk already exists
		existingChunkData, chunkReadError :=
			os.ReadFile(chunkFilePath)

		if chunkReadError == nil {

			// Generate SHA-256 hash for the existing stored chunk
			existingChunkHash :=
				sha256.Sum256(existingChunkData)

			existingChunkHashString :=
				fmt.Sprintf("%x", existingChunkHash)

			// Compare old chunk hash with new chunk hash
			if existingChunkHashString == chunk.Hash {

				fmt.Println(
					"CHUNK UNCHANGED: ",
					chunkFileName,
				)

				// Skip saving because the chunk did not change
				continue
			}

			fmt.Println(
				"CHANGED/NEW CHUNK:",
				chunkFileName,
			)
		}

		// Save chunk if it is new or has changed
		chunkSaveError := os.WriteFile(
			chunkFilePath,
			chunk.Data,
			0644,
		)

		if chunkSaveError != nil {
			fmt.Println(
				"Chunk saving error:",
				chunkSaveError,
			)
			return
		}

	}

	// Check for old chunks that are no longer needed
	existingChunkFiles, readDirectoryError :=
		os.ReadDir(fileChunkFolder)

	if readDirectoryError != nil {
		fmt.Println(
			"Chunk folder reading error:",
			readDirectoryError,
		)
		return
	}

	// Remove extra old chunks if the modified file became smaller
	for index := len(chunks); index < len(existingChunkFiles); index++ {

		oldChunkFileName := fmt.Sprintf(
			"chunk_%d.chunk",
			index,
		)

		oldChunkFilePath := filepath.Join(
			fileChunkFolder,
			oldChunkFileName,
		)

		removeChunkError := os.Remove(
			oldChunkFilePath,
		)

		if removeChunkError != nil {
			fmt.Println(
				"Old chunk removal error:",
				removeChunkError,
			)
			return
		}

		fmt.Println(
			"Removed old chunk:",
			oldChunkFileName,
		)
	}
}
