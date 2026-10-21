// / Rabindra Neupane
// ReconstructFile reconstructs a file from its chunks stored in the chunk storage path.
package network

import (
	"fmt"
	"os"
	"path/filepath"
)

// ReconstructFile reconstructs a file from its chunks stored in the chunk storage path.
func ReconstructFile(projectDirectory string, fileName string, totalChunks int) error {

	fileName = filepath.Base(fileName)

	// Folder containing received chunks
	chunkFolder := filepath.Join(projectDirectory, "File Chunks", fileName)
	// final reconstructed file
	syncFolder := filepath.Join(projectDirectory, "Synced Files")

	// Ensure the sync folder exists before creating the output file
	if err := os.MkdirAll(syncFolder, 0755); err != nil {
		return err
	}

	// Ensure the chunk folder exists before reading chunks
	outputPath := filepath.Join(syncFolder, fileName)
	fmt.Printf("Reconstructing %s from %d chunks... \n", fileName, totalChunks)

	// Create the output file for writing reconstructed chunks
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create reconstructed file %w", err)
	}
	defer outputFile.Close()

	// Read and write each chunk sequentially to reconstruct the file
	for chunkIndex := 0; chunkIndex < totalChunks; chunkIndex++ {
		chunkPath := filepath.Join(chunkFolder, fmt.Sprintf("chunk_%d.chunk", chunkIndex))

		// Read the chunk data from the chunk file
		chunkData, err := os.ReadFile(chunkPath)
		if err != nil {
			return fmt.Errorf("failed to read chunk %d of %s: %w", chunkIndex, fileName, err)
		}

		// Write the chunk data to the output file
		_, err = outputFile.Write(chunkData)
		if err != nil {
			return fmt.Errorf("failed to write chunk %d of %s: %w", chunkIndex, fileName, err)
		}
	}

	// All chunks have been written, file reconstruction is complete
	fmt.Printf("File Reconstructed successfully.%s\n", outputPath)

	return nil
}
