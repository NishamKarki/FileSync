package main

import (
	"crypto/sha256"
	"encoding/hex"
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

// Splits a file into fixed-size chunks and computes a SHA-256 hash for each one.
func ChunkFile(filePath string) ([]Chunk, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var chunks []Chunk
	buffer := make([]byte, chunkSize)
	index := 0

	for {
		bytesRead, err := file.Read(buffer)
		if bytesRead > 0 {
			data := make([]byte, bytesRead)
			copy(data, buffer[:bytesRead])

			hash := sha256.Sum256(data)

			chunks = append(chunks, Chunk{
				Index: index,
				Data:  data,
				Hash:  hex.EncodeToString(hash[:]),
			})
			index++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return chunks, nil
}
