package main

import (
	"fmt"
	"io"
	"os"
)

/////// Nisham Karki
// File Chunking: First initialize a fixed sized chunk
// Open a file and read it
// Create a structure that stores the data of a file in a chunk, and give it
// 		an id/index for chunk locating/indentifying
// Begin reading the file data worth the size of a chunk and save in the structure
// Move to the remaining data of the file
// Repeat until the file is fully reads and store in chunks

// Initial fixed size chunk set to 1 KB (1024)
const chunkSize = 1024

// Chunk that represents a fixed size chunk
type Chunk struct {
	Index int
	Data  []byte
}

// Function that divides a file into chunks
func (app *App) ChunkFile(filePath string) []Chunk {
	// An array that store chunks created from a file
	var fileChunks []Chunk

	// Open a file that needs to be chunked
	fileToChunk, fileOpenError := os.Open(filePath)

	// Check error during file opening
	if fileOpenError != nil {
		fmt.Print("File opening error: ", fileOpenError)
	}

	defer fileToChunk.Close()

	// Initialize chunk index
	ChunkIndex := 0

	for {
		// Create a temporary storage to hold a chunk
		chunkBuffer := make([]byte, chunkSize)

		// Read only up to the fixed size of the chunk from the file
		totalBytesRead, fileReadingError := fileToChunk.Read(chunkBuffer)

		if totalBytesRead > 0 {

			// Create chunk data that contains only the byte that were read from the file
			chunkData := make([]byte, totalBytesRead)

			// Use string slicing to slice the chunked portion from the buffer
			// and store it into chunkData
			copy(chunkData, chunkBuffer[:totalBytesRead])

			// Create the chunk
			newChunk := Chunk{Index: ChunkIndex, Data: chunkData}

			// Add the chunk to the chunk list
			fileChunks = append(fileChunks, newChunk)

			// Printing for testing
			fmt.Println("Chunk: ", ChunkIndex, "\nBytes read: ", totalBytesRead)
		}

		ChunkIndex++

		// Testing for end-of-file.
		// EOF means file chunking is complete
		if fileReadingError == io.EOF {
			break
		} else {
			fmt.Println("File reading error: ", fileReadingError)
		}
	}

	fileInfo, error := fileToChunk.Stat()
	if error != nil {
		fmt.Println("")
	}

	fmt.Println("\nFile size: ", fileInfo.Size(),
		"\nTotal Chunks gotten: ", len(fileChunks))

	return fileChunks
}
