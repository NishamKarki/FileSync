// Author: Nisham Karki

package main

import (
	"FileSyncWails/network"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// StoredChunk represents a chunk file that already exists
// inside the local File Chunks folder.
type StoredChunk struct {
	Index    int
	FilePath string
}

// ReconstructFile rebuilds the original file from its stored chunks.
//
// FileSync finds the folder belonging to the selected file,
// reads all chunk files from that folder, arranges them by their
// numerical chunk index, and writes them into one complete file.
//
// The reconstructed file is first created as a temporary file outside
// the Synced Files folder. This prevents FileSync from leaving a
// partially reconstructed file in the sync folder if reconstruction fails.
//
// After every chunk is successfully written, the temporary file
// replaces the existing file inside the Synced Files folder.
func (app *App) ReconstructFile(fileName string) error {
	//Rabindra Neupane
	return app.reconstructFile(fileName, nil)
}

//Rabindra Neupane
// Reconstruction implementation: Nisham Karki
// Transfer size and hash verification: Rabindra Neupane
func (app *App) reconstructFile(fileName string, metadata *network.FileMetadata) error {

	// Use only the actual file name to prevent folder paths
	// from being included in the reconstruction request.
	fileName = filepath.Base(fileName)

	// Locate the folder containing the stored chunks for this file.
	fileChunkFolder := filepath.Join(
		app.chunkStoragePath,
		fileName,
	)

	// Read all files stored inside the file's chunk folder.
	chunkFolderContents, chunkFolderReadError :=
		os.ReadDir(fileChunkFolder)

	if chunkFolderReadError != nil {
		return fmt.Errorf(
			"failed to read chunk folder: %w",
			chunkFolderReadError,
		)
	}

	// Store the valid chunk files found inside the folder.
	var storedChunks []StoredChunk

	for index := 0; index < len(chunkFolderContents); index++ {

		chunkFolderEntry := chunkFolderContents[index]

		// Ignore folders because reconstruction only needs chunk files.
		if chunkFolderEntry.IsDir() {
			continue
		}

		chunkFileName := chunkFolderEntry.Name()

		// Ignore anything that does not follow the FileSync
		// chunk naming format: chunk_NUMBER.chunk
		if !strings.HasPrefix(
			chunkFileName,
			"chunk_",
		) {
			continue
		}

		if !strings.HasSuffix(
			chunkFileName,
			".chunk",
		) {
			continue
		}

		// Remove the text around the chunk number.
		chunkIndexText := strings.TrimPrefix(
			chunkFileName,
			"chunk_",
		)

		chunkIndexText = strings.TrimSuffix(
			chunkIndexText,
			".chunk",
		)

		// Convert the chunk number from text into an integer.
		chunkIndex, chunkIndexConversionError :=
			strconv.Atoi(chunkIndexText)

		if chunkIndexConversionError != nil {
			continue
		}

		// Save the chunk index and its complete path.
		storedChunk := StoredChunk{
			Index: chunkIndex,
			FilePath: filepath.Join(
				fileChunkFolder,
				chunkFileName,
			),
		}

		storedChunks = append(
			storedChunks,
			storedChunk,
		)
	}

	// If no chunks exist, check whether the original file is actually empty.
	// An empty file correctly produces zero chunks, so FileSync should
	// reconstruct it as an empty file instead of treating it as an error.
	//Rabindra Neupane
	if metadata != nil && len(storedChunks) != len(metadata.ChunkHashes) {
		return fmt.Errorf("stored chunk count does not match transfer metadata")
	}
	if len(storedChunks) == 0 && metadata == nil {

		originalFilePath := filepath.Join(
			app.syncFolderPath,
			fileName,
		)

		originalFileInfo, originalFileInfoError :=
			os.Stat(originalFilePath)

		if originalFileInfoError != nil {
			return fmt.Errorf(
				"failed to check original file: %w",
				originalFileInfoError,
			)
		}

		// Zero-byte files do not need chunks.
		if originalFileInfo.Size() == 0 {

			reconstructedFilePath := filepath.Join(
				app.reconstructedFilesPath,
				fileName,
			)

			// Create or truncate the reconstructed file so it becomes empty.
			emptyFile, emptyFileCreationError :=
				os.Create(reconstructedFilePath)

			if emptyFileCreationError != nil {
				return fmt.Errorf(
					"failed to create empty reconstructed file: %w",
					emptyFileCreationError,
				)
			}

			emptyFile.Close()

			fmt.Println(
				"EMPTY FILE RECONSTRUCTED:",
				reconstructedFilePath,
			)

			return nil
		}

		// A non-empty file should always have at least one chunk.
		return fmt.Errorf(
			"no chunks found for non-empty file: %s",
			fileName,
		)
	}

	// Sort chunks using their numerical index.
	//
	// This is important because alphabetical sorting could produce:
	// chunk_1, chunk_10, chunk_11, chunk_2
	//
	// Numerical sorting produces the correct reconstruction order:
	// chunk_0, chunk_1, chunk_2 ... chunk_10
	sort.Slice(
		storedChunks,
		func(firstIndex int, secondIndex int) bool {
			return storedChunks[firstIndex].Index <
				storedChunks[secondIndex].Index
		},
	)

	// Create the reconstructed file temporarily inside File Chunks.
	// This folder is not watched by the FileSync file watcher.
	temporaryFile, temporaryFileCreationError :=
		os.CreateTemp(
			app.reconstructedFilesPath,
			"reconstruct-*",
		)

	if temporaryFileCreationError != nil {
		return fmt.Errorf(
			"failed to create temporary reconstructed file: %w",
			temporaryFileCreationError,
		)
	}

	temporaryFilePath := temporaryFile.Name()

	// Remove the temporary file if reconstruction fails before completion.
	defer os.Remove(temporaryFilePath)

	// Write each chunk into the temporary file in numerical order.
	for index := 0; index < len(storedChunks); index++ {

		storedChunk := storedChunks[index]

		// Every chunk should appear consecutively starting from zero.
		// If one is missing, rebuilding the file would create corrupted data.
		if storedChunk.Index != index {

			temporaryFile.Close()

			return fmt.Errorf(
				"missing chunk %d while reconstructing %s",
				index,
				fileName,
			)
		}

		// Open the next stored chunk.
		chunkFile, chunkOpenError :=
			os.Open(storedChunk.FilePath)

		if chunkOpenError != nil {

			temporaryFile.Close()

			return fmt.Errorf(
				"failed to open chunk %d: %w",
				storedChunk.Index,
				chunkOpenError,
			)
		}

		// Append the chunk data to the reconstructed file.
		_, chunkCopyError :=
			io.Copy(
				temporaryFile,
				chunkFile,
			)

		chunkFile.Close()

		if chunkCopyError != nil {

			temporaryFile.Close()

			return fmt.Errorf(
				"failed to write chunk %d: %w",
				storedChunk.Index,
				chunkCopyError,
			)
		}

		fmt.Println(
			"RECONSTRUCTED CHUNK:",
			storedChunk.Index,
		)
	}

	// Close the completed temporary file before moving it.
	temporaryFileCloseError :=
		temporaryFile.Close()

	if temporaryFileCloseError != nil {
		return temporaryFileCloseError
	}

	//Rabindra Neupane
	// Verify the temporary output before touching an existing reconstructed file.
	if metadata != nil {
		output, err := os.Open(temporaryFilePath)
		if err != nil {
			return err
		}
		hasher := sha256.New()
		size, readError := io.Copy(hasher, output)
		closeError := output.Close()
		if readError != nil {
			return readError
		}
		if closeError != nil {
			return closeError
		}
		if size != metadata.FileSize || hex.EncodeToString(hasher.Sum(nil)) != metadata.FileHash {
			return fmt.Errorf("reconstructed file failed size or SHA-256 verification")
		}
	}

	// Create the final location of the reconstructed file.
	destinationFilePath := filepath.Join(
		app.reconstructedFilesPath,
		fileName,
	)

	// Remove the existing version only after the complete replacement
	// file has been successfully reconstructed.
	// Try several times to remove an older reconstructed copy.
	// Some applications may temporarily keep the file locked
	// while they finish reading or writing it.
	var existingFileRemovalError error

	for attempt := 0; attempt < 5; attempt++ {

		existingFileRemovalError =
			os.Remove(destinationFilePath)

		// File was removed successfully.
		if existingFileRemovalError == nil {
			break
		}

		// There is nothing to remove if the file does not already exist.
		if os.IsNotExist(existingFileRemovalError) {
			existingFileRemovalError = nil
			break
		}

		fmt.Println(
			"Reconstructed file is currently busy. Retrying...",
		)

		// Wait before trying again.
		time.Sleep(
			500 * time.Millisecond,
		)
	}

	if existingFileRemovalError != nil {
		return fmt.Errorf(
			"failed to replace existing reconstructed file: %w",
			existingFileRemovalError,
		)
	}

	// Move the completed reconstructed file into Reconstructed Files folder
	fileMoveError :=
		os.Rename(
			temporaryFilePath,
			destinationFilePath,
		)

	if fileMoveError != nil {
		return fmt.Errorf(
			"failed to move reconstructed file: %w",
			fileMoveError,
		)
	}

	fmt.Println(
		"FILE RECONSTRUCTED:",
		destinationFilePath,
	)

	return nil
}
