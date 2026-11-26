// Author: Rabindra Neupane
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Rabindra Neupane
// reportLocalChunkInventory reports every verified chunk currently stored here.
// The coordinator uses this complete list to locate available peer copies.
func (app *App) reportLocalChunkInventory() error {
	// Skip reporting until this device has a registered coordinator identity.
	if app.deviceRegistry == nil || !app.deviceRegistry.Status().Registered {
		return nil
	}

	// Read the accepted per-file chunk folders from local storage.
	fileFolders, folderReadingError := os.ReadDir(app.chunkStoragePath)
	if folderReadingError != nil {
		return fmt.Errorf("read local chunk storage: %w", folderReadingError)
	}

	// Hash each stored chunk and keep each content identity only once.
	uniqueHashes := make(map[string]struct{})
	for _, fileFolder := range fileFolders {
		// Ignore transfer staging and non-folder entries because they are not accepted chunks.
		if !fileFolder.IsDir() || fileFolder.Name() == ".transfers" {
			continue
		}
		chunkFolderPath := filepath.Join(app.chunkStoragePath, fileFolder.Name())
		chunkFiles, chunkFolderReadingError := os.ReadDir(chunkFolderPath)
		if chunkFolderReadingError != nil {
			return fmt.Errorf("read chunk folder %s: %w", fileFolder.Name(), chunkFolderReadingError)
		}
		for _, chunkFile := range chunkFiles {
			// Report only the normal chunk files produced by FileSync.
			if chunkFile.IsDir() || !strings.HasSuffix(chunkFile.Name(), ".chunk") {
				continue
			}
			chunkHash, chunkHashingError := hashStoredFile(filepath.Join(chunkFolderPath, chunkFile.Name()))
			if chunkHashingError != nil {
				return fmt.Errorf("hash stored chunk %s: %w", chunkFile.Name(), chunkHashingError)
			}
			uniqueHashes[chunkHash] = struct{}{}
		}
	}

	// Use stable ordering so identical inventories produce predictable requests.
	chunkHashes := make([]string, 0, len(uniqueHashes))
	for chunkHash := range uniqueHashes {
		chunkHashes = append(chunkHashes, chunkHash)
	}
	sort.Strings(chunkHashes)

	// Bound inventory reporting so a slow coordinator does not block FileSync.
	reportContext := app.ctx
	if reportContext == nil {
		reportContext = context.Background()
	}
	reportContext, cancelReport := context.WithTimeout(reportContext, 30*time.Second)
	defer cancelReport()
	return app.deviceRegistry.ReportChunks(reportContext, chunkHashes)
}

// Rabindra Neupane
// scheduleLocalChunkInventoryReport updates locations without delaying a sync.
func (app *App) scheduleLocalChunkInventoryReport() {
	// Report in the background because verified file handling is already complete.
	go func() {
		if inventoryReportingError := app.reportLocalChunkInventory(); inventoryReportingError != nil {
			fmt.Println("Chunk inventory report error:", inventoryReportingError)
		}
	}()
}

// FetchMissingChunkFromPeers retrieves one verified chunk and installs it in
// the local cache so reconstruction can continue when the original sender is offline.
func (app *App) FetchMissingChunkFromPeers(requestContext context.Context, fileName string, chunkIndex int, chunkHash string) error {
	// Reject unsafe target paths before creating any local storage.
	if fileName == "" || filepath.Base(fileName) != fileName || chunkIndex < 0 {
		return fmt.Errorf("invalid missing chunk target")
	}
	// Require an active coordinator identity for secure peer location lookup.
	if app.deviceRegistry == nil {
		return fmt.Errorf("chunk retrieval requires device registration")
	}
	// Use the application context when a caller did not provide one.
	if requestContext == nil {
		requestContext = app.ctx
	}
	if requestContext == nil {
		requestContext = context.Background()
	}
	// Ask the coordinator for a peer copy and keep its source validation intact.
	chunkData, _, chunkRetrievalError := app.deviceRegistry.RetrieveChunk(requestContext, chunkHash)
	if chunkRetrievalError != nil {
		return fmt.Errorf("retrieve missing chunk: %w", chunkRetrievalError)
	}
	// Verify again at the application boundary before accepting peer data.
	retrievedHash := sha256.Sum256(chunkData)
	if hex.EncodeToString(retrievedHash[:]) != chunkHash {
		return fmt.Errorf("retrieved chunk hash does not match requested hash")
	}
	// Create the accepted cache directory for the reconstructed file.
	fileChunkDirectory := filepath.Join(app.chunkStoragePath, fileName)
	if directoryCreationError := os.MkdirAll(fileChunkDirectory, 0755); directoryCreationError != nil {
		return fmt.Errorf("create missing chunk directory: %w", directoryCreationError)
	}
	// Stage the bytes beside the final path so a partial write is never visible.
	temporaryChunk, temporaryCreationError := os.CreateTemp(fileChunkDirectory, ".chunk-*")
	if temporaryCreationError != nil {
		return fmt.Errorf("stage missing chunk: %w", temporaryCreationError)
	}
	temporaryPath := temporaryChunk.Name()
	defer os.Remove(temporaryPath)
	if _, chunkWritingError := temporaryChunk.Write(chunkData); chunkWritingError != nil {
		temporaryChunk.Close()
		return fmt.Errorf("write missing chunk: %w", chunkWritingError)
	}
	if temporaryClosingError := temporaryChunk.Close(); temporaryClosingError != nil {
		return fmt.Errorf("close missing chunk: %w", temporaryClosingError)
	}
	// Publish the verified cache entry with the index expected by reconstruction.
	finalChunkPath := filepath.Join(fileChunkDirectory, fmt.Sprintf("chunk_%d.chunk", chunkIndex))
	if chunkRenameError := os.Rename(temporaryPath, finalChunkPath); chunkRenameError != nil {
		return fmt.Errorf("install missing chunk: %w", chunkRenameError)
	}
	// Report the new local copy without failing a successful recovery when offline.
	app.scheduleLocalChunkInventoryReport()
	return nil
}
