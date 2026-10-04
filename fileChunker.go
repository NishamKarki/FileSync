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

	defer fileToChunk.Close()

	chunkIndex := 0

	for {

		// Temporary buffer for one fixed-size chunk
		chunkBuffer := make(
			[]byte,
			chunkSize,
		)

		// Read up to 1 KB
		totalBytesRead, fileReadingError :=
			fileToChunk.Read(chunkBuffer)

		if totalBytesRead > 0 {

			// Keep only bytes actually read
			chunkData := make(
				[]byte,
				totalBytesRead,
			)

			copy(
				chunkData,
				chunkBuffer[:totalBytesRead],
			)

			// Generate SHA-256 AFTER actual data is copied
			chunkHash :=
				sha256.Sum256(chunkData)

			newChunk := Chunk{
				Index: chunkIndex,
				Data:  chunkData,
				Hash:  fmt.Sprintf("%x", chunkHash),
			}

			fileChunks = append(
				fileChunks,
				newChunk,
			)

			fmt.Println(
				"Chunk:",
				chunkIndex,
				"\nBytes read:",
				totalBytesRead,
			)

			chunkIndex++
		}

		if fileReadingError == io.EOF {
			break
		}

		if fileReadingError != nil {

			fmt.Println(
				"File reading error:",
				fileReadingError,
			)

			return fileChunks, fileReadingError
		}
	}

	fmt.Println(
		"Total Chunks gotten:",
		len(fileChunks),
	)

	return fileChunks, nil
}
