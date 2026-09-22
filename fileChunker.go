package main

/////// Nisham Karki

// Initial fixed size chunk set to 1 KB (1024)
const chunkSize = 1024

type Chunk struct {
	Index int
	Data  []byte
}

// func ChunkFile(filePath string) ([]Chunk)
