// Author: Nisham Karki

package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// Initial fixed size chunk set to 1 KB (1024)
const chunkSize = 1024

// Chunk that represents a fixed size chunk
type Chunk struct {
	Index int
	Data  []byte
	Hash  string // SHA-256 hash of Data, hex-encoded
}

// Function that divides a file into chunks
func (app *App) ChunkFile(filePath string) ([]Chunk, error) {
	// An array that store chunks created from a file
	var fileChunks []Chunk

	// Open a file that needs to be chunked
	fileToChunk, fileOpenError := os.Open(filePath)

	// Check error during file opening
	if fileOpenError != nil {
		fmt.Print("File opening error: ", fileOpenError)
		return fileChunks, fileOpenError
	}

	// Closes the file when chunking is complete
	defer fileToChunk.Close()

	// Start chunk number at 0
	chunkIndex := 0

	for {

		// Ceate a temporary buffer for one fixed-size chunk
		chunkBuffer := make(
			[]byte,
			chunkSize,
		)

		// Read up to 1 KB of data from a file
		totalBytesRead, fileReadingError :=
			fileToChunk.Read(chunkBuffer)

		// If data read is more than 0, begin chunking
		if totalBytesRead > 0 {

			// Store data into chunkData of only the bytes actually read
			chunkData := make(
				[]byte,
				totalBytesRead,
			)

			// Copy the file data from the buffer into the chunk
			copy(
				chunkData,
				chunkBuffer[:totalBytesRead],
			)

			// Generate SHA-256 from the chunk dataa
			// This hash will be later compared to detect changed chunks
			chunkHash :=
				sha256.Sum256(chunkData)

			// Create a new chunk containing its index, data, hash
			newChunk := Chunk{
				Index: chunkIndex,
				Data:  chunkData,
				Hash:  fmt.Sprintf("%x", chunkHash),
			}

			// Add the chunk to the list of file chunks
			fileChunks = append(
				fileChunks,
				newChunk,
			)

			// Display chunk information for testing
			fmt.Println(
				"Chunk:",
				chunkIndex,
				"\nBytes read:",
				totalBytesRead,
			)

			chunkIndex++
		}

		// Testing for end-of-file.
		// EOF means file chunking is complete
		if fileReadingError == io.EOF {
			break
		}

		// Print any file reading error to terminal
		if fileReadingError != nil {

			fmt.Println(
				"File reading error:",
				fileReadingError,
			)

			return fileChunks, fileReadingError
		}
	}

	// Prints chunk information
	fmt.Println(
		"Total Chunks gotten:",
		len(fileChunks),
	)

	return fileChunks, nil
}
