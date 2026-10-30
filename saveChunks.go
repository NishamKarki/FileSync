// Author: Nisham Karki

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// SaveChunks creates individual folder for each file that are chunks
// in local "File Chunks" directory
// Each synchronized files get their own chunk folder
// If modified files become smaller, and produce less chunks, old chunks are removed
// Rabindra Neupane
// Preserve Nisham Karki's public method while internal callers receive failures.
func (app *App) SaveChunks(filePath string, chunks []Chunk) {
	if chunkSavingError := app.saveChunks(filePath, chunks); chunkSavingError != nil {
		fmt.Println("Chunk saving error:", chunkSavingError)
	}
}

// Rabindra Neupane
// Chunk storage implementation: Nisham Karki. Error propagation: Rabindra Neupane.
func (app *App) saveChunks(filePath string, chunks []Chunk) error {

	// Get file name
	fileName := filepath.Base(filePath)

	// Create a folder to keep the chunks for each file
	fileChunkFolder := filepath.Join(
		app.chunkStoragePath,
		fileName,
	)

	// Create chunk folder, if it does not exist
	chunkFolderCreationError := os.MkdirAll(
		fileChunkFolder,
		0755,
	)

	if chunkFolderCreationError != nil {
		fmt.Println(
			"Chunk folder creation error:",
			chunkFolderCreationError,
		)
		return chunkFolderCreationError
	}

	// Process each chunk generated from the file
	for index := 0; index < len(chunks); index++ {

		chunk := chunks[index]

		// Create chunk file name
		// Ex: "chunk_0.chunk", "chunk_1.chunk", and so on
		chunkFileName := fmt.Sprintf(
			"chunk_%d.chunk",
			chunk.Index,
		)

		// Create chunk file path where the chunk will be stored
		chunkFilePath := filepath.Join(
			fileChunkFolder,
			chunkFileName,
		)

		// Read an existing version of the chunk for the lcoal storage
		existingChunkData, existingChunkReadingError :=
			os.ReadFile(chunkFilePath)

		// Compare hash value with the new chunk, if the chunk already exists
		if existingChunkReadingError == nil {

			// Generate SHA-256 hash for the existing stored chunk
			existingChunkHash :=
				sha256.Sum256(existingChunkData)

			// Convert the existing hash into hexadecimal format used by ChunkFile
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
		chunkSavingError := os.WriteFile(
			chunkFilePath,
			chunk.Data,
			0644,
		)

		if chunkSavingError != nil {
			fmt.Println(
				"Chunk saving error:",
				chunkSavingError,
			)
			return chunkSavingError
		}

	}

	// Read the chunk folder to check if the old chunks remain after a files
	// becomes smaller due to modification
	existingChunkFiles, chunkDirectoryReadingError :=
		os.ReadDir(fileChunkFolder)

	if chunkDirectoryReadingError != nil {
		fmt.Println(
			"Chunk folder reading error:",
			chunkDirectoryReadingError,
		)
		return chunkDirectoryReadingError
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

		oldChunkRemovalError := os.Remove(
			oldChunkFilePath,
		)

		if oldChunkRemovalError != nil {
			fmt.Println(
				"Old chunk removal error:",
				oldChunkRemovalError,
			)
			return oldChunkRemovalError
		}

		fmt.Println(
			"Removed old chunk:",
			oldChunkFileName,
		)
	}
	return nil
}
