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
func (app *App) SaveChunks(filePath string, chunks []Chunk) {

	// Get file name
	fileName := filepath.Base(filePath)

	// Create a folder to keep the chunks for each file
	fileChunkFolder := filepath.Join(
		app.chunkStoragePath,
		fileName,
	)

	// Create chunk folder, if it does not exist
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
		existingChunkData, chunkReadError :=
			os.ReadFile(chunkFilePath)

		// Compare hash value with the new chunk, if the chunk already exists
		if chunkReadError == nil {

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

	// Read the chunk folder to check if the old chunks remain after a files
	// becomes smaller due to modification
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
