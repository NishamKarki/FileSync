package main

import (
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

	// Create a folder to keep the chunks for each files
	fileChunkFolder := filepath.Join(
		app.chunkStoragePath,
		fileName,
	)

	// Create a folder to keep chunk folders
	os.MkdirAll(fileChunkFolder, 0755)

	for index := 0; index < len(chunks); index++ {

		chunk := chunks[index]

		// Create chunk file name
		chunkFileName := fmt.Sprintf("chunk_%d.chunk", chunk.Index)

		// Create chunk file path
		chunkFilePath := filepath.Join(fileChunkFolder, chunkFileName)

		// Save chunk locally inside the FileSync directory
		os.WriteFile(chunkFilePath, chunk.Data, 0644)

	}
}
