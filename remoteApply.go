package main

import (
	"FileSyncWails/network"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Rabindra Neupane
func hashStoredFile(path string) (string, error) {
	file, fileOpeningError := os.Open(path)
	if fileOpeningError != nil {
		return "", fileOpeningError
	}
	defer file.Close()
	hasher := sha256.New()
	if _, fileHashingError := io.Copy(hasher, file); fileHashingError != nil {
		return "", fileHashingError
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// Rabindra Neupane
// Caller holds fileProcessingMutex. Keep the marker while contents match so
// delayed/duplicate watcher events are ignored. A real edit removes the marker.
func (app *App) matchesRemoteFile(path string) bool {
	path = filepath.Clean(path)
	expected, exists := app.remoteFileHashes[path]
	if !exists {
		return false
	}
	actualHash, fileHashingError := hashStoredFile(path)
	if fileHashingError != nil {
		if os.IsNotExist(fileHashingError) {
			delete(app.remoteFileHashes, path)
		}
		return false
	}
	if actualHash == expected {
		return true
	}
	delete(app.remoteFileHashes, path)
	return false
}

// Rabindra Neupane
// Reconstruction: Nisham Karki. Version schema: Santosh Tripathee.
// Remote application and watcher coordination: Rabindra Neupane.
func (app *App) applyIncomingTransfer(metadata network.FileMetadata, chunkRoot string) error {
	app.fileProcessingMutex.Lock()
	defer app.fileProcessingMutex.Unlock()
	if app.db == nil || app.syncFolderPath == "" {
		return fmt.Errorf("receiver storage or metadata database is unavailable")
	}
	if metadata.FileID != metadata.FileName || metadata.FileName == "" || filepath.Base(metadata.FileName) != metadata.FileName || metadata.Version < 1 {
		return fmt.Errorf("invalid received file identity or version")
	}
	// Reconstruct incoming transfers in isolation before applying them to Synced Files.
	outputRoot, outputDirectoryCreationError := os.MkdirTemp(chunkRoot, "verified-output-")
	if outputDirectoryCreationError != nil {
		return outputDirectoryCreationError
	}
	cleanupOutput := true
	defer func() {
		if cleanupOutput {
			os.RemoveAll(outputRoot)
		}
	}()
	transferApp := &App{chunkStoragePath: chunkRoot, reconstructedFilesPath: outputRoot}
	if reconstructionError := transferApp.reconstructStoredFile(metadata.FileName, &metadata); reconstructionError != nil {
		return reconstructionError
	}
	verified := filepath.Join(outputRoot, metadata.FileName)
	if syncFolderCreationError := os.MkdirAll(app.syncFolderPath, 0755); syncFolderCreationError != nil {
		return syncFolderCreationError
	}
	destination := filepath.Join(app.syncFolderPath, metadata.FileName)
	// Conflict decision: Santosh Tripathee. Hash actual bytes, not lagging metadata.
	previousHash, destinationHashingError := HashFileOnDisk(destination)
	if destinationHashingError != nil {
		return destinationHashingError
	}
	hadPrevious := previousHash != ""
	switch DetectConflict(metadata.BaseHash, previousHash, metadata.FileHash) {
	case KeepLocal:
		return &network.KeepLocalError{}
	case Conflict:
		return app.preserveIncomingConflict(metadata, verified)
	case InSync, AcceptRemote:
		// Verification succeeded; record content and shared base together below.
	default:
		return fmt.Errorf("unknown conflict decision")
	}
	// Preserve a rollback copy outside the watched folder before any replacement.
	backupRoot, backupDirectoryCreationError := os.MkdirTemp(outputRoot, "backup-")
	if backupDirectoryCreationError != nil {
		return backupDirectoryCreationError
	}
	backup := filepath.Join(backupRoot, "previous-file")
	if hadPrevious {
		if backupCopyingError := copyRemoteBackup(destination, backup); backupCopyingError != nil {
			return backupCopyingError
		}
	}
	databaseTransaction, transactionStartError := app.db.Begin()
	if transactionStartError != nil {
		return transactionStartError
	}
	defer databaseTransaction.Rollback()
	if versionRecordingError := app.recordReceivedVersion(databaseTransaction, metadata); versionRecordingError != nil {
		return versionRecordingError
	}
	// Recheck after metadata work so a user edit during verification isn't lost.
	currentHash, destinationRecheckHashingError := hashStoredFile(destination)
	if (hadPrevious && (destinationRecheckHashingError != nil || currentHash != previousHash)) || (!hadPrevious && !os.IsNotExist(destinationRecheckHashingError)) {
		return fmt.Errorf("destination changed during transfer; retry after local processing")
	}
	if fileReplacementError := replaceVerifiedFile(verified, destination); fileReplacementError != nil {
		return fileReplacementError
	}
	if transactionCommitError := databaseTransaction.Commit(); transactionCommitError != nil {
		// File and SQLite cannot share one atomic transaction. Restore the prior
		// file if the database commit fails, and report any rollback failure.
		var fileRestoreError error
		if hadPrevious {
			fileRestoreError = replaceVerifiedFile(backup, destination)
		} else {
			fileRestoreError = os.Remove(destination)
		}
		if fileRestoreError != nil {
			cleanupOutput = false
			return fmt.Errorf("metadata commit failed: %v; file rollback failed: %v; recovery files retained at %s", transactionCommitError, fileRestoreError, outputRoot)
		}
		if hadPrevious {
			if app.remoteFileHashes == nil {
				app.remoteFileHashes = make(map[string]string)
			}
			app.remoteFileHashes[filepath.Clean(destination)] = previousHash
		}
		return fmt.Errorf("received metadata commit failed: %w", transactionCommitError)
	}
	if app.remoteFileHashes == nil {
		app.remoteFileHashes = make(map[string]string)
	}
	app.remoteFileHashes[filepath.Clean(destination)] = metadata.FileHash
	fmt.Printf("Received and verified %s in Synced Files (sender version %d; recorded with receiver's local sequence)\n", metadata.FileName, metadata.Version)
	return nil
}

// Rabindra Neupane
func copyRemoteBackup(source, destination string) error {
	input, sourceOpeningError := os.Open(source)
	if sourceOpeningError != nil {
		return sourceOpeningError
	}
	defer input.Close()
	output, destinationCreationError := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if destinationCreationError != nil {
		return destinationCreationError
	}
	_, backupCopyingError := io.Copy(output, input)
	backupFileClosingError := output.Close()
	if backupCopyingError != nil {
		os.Remove(destination) // Exclusively created by this call; never a preexisting copy.
		return backupCopyingError
	}
	if backupFileClosingError != nil {
		os.Remove(destination)
	}
	return backupFileClosingError
}

// Rabindra Neupane
// Version schema: Santosh Tripathee. Receiver mapping: Rabindra Neupane.
// Sender counters are not shared revision IDs. Accepted contents get a local
// sequence number and local parent; repeated identical contents reuse the row.
func (app *App) recordReceivedVersion(databaseTransaction *sql.Tx, metadata network.FileMetadata) error {
	var current int
	currentVersionQueryError := databaseTransaction.QueryRow("SELECT current_version FROM files WHERE id = ?", metadata.FileID).Scan(&current)
	if currentVersionQueryError != nil && currentVersionQueryError != sql.ErrNoRows {
		return currentVersionQueryError
	}
	known := currentVersionQueryError == nil
	if known {
		var id int64
		if versionIDQueryError := databaseTransaction.QueryRow("SELECT id FROM file_versions WHERE file_id = ? AND version_number = ? ORDER BY id DESC LIMIT 1", metadata.FileID, current).Scan(&id); versionIDQueryError != nil {
			return versionIDQueryError
		}
		incoming := make([]Chunk, len(metadata.ChunkHashes))
		for index, hash := range metadata.ChunkHashes {
			incoming[index] = Chunk{Index: index, Hash: hash}
		}
		comparison := compareRecordedChunks(databaseTransaction, id, incoming)
		if comparison == nil {
			if _, versionHashUpdateError := databaseTransaction.Exec("UPDATE file_versions SET content_hash=? WHERE id=?", metadata.FileHash, id); versionHashUpdateError != nil {
				return versionHashUpdateError
			}
			_, fileRecordUpdateError := databaseTransaction.Exec("UPDATE files SET size=?, last_synced_hash=?, modified_by=? WHERE id=?", metadata.FileSize, metadata.FileHash, metadata.DeviceID, metadata.FileID)
			return fileRecordUpdateError
		}
		if !errors.Is(comparison, errVersionChunksDiffer) {
			return comparison
		}
	}
	var maximum sql.NullInt64
	if maximumVersionQueryError := databaseTransaction.QueryRow("SELECT MAX(version_number) FROM file_versions WHERE file_id = ?", metadata.FileID).Scan(&maximum); maximumVersionQueryError != nil {
		return maximumVersionQueryError
	}
	next := current + 1
	if maximum.Valid && int64(next) <= maximum.Int64 {
		next = int(maximum.Int64) + 1
	}
	_, fileRecordSavingError := databaseTransaction.Exec(`INSERT INTO files (id, path, name, current_version, size, modified_by, last_synced_hash)
        VALUES (?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET current_version=excluded.current_version,
        size=excluded.size, modified_by=excluded.modified_by, last_synced_hash=excluded.last_synced_hash, updated_at=CURRENT_TIMESTAMP`, metadata.FileID, metadata.FileID, metadata.FileName, next, metadata.FileSize, metadata.DeviceID, metadata.FileHash)
	if fileRecordSavingError != nil {
		return fileRecordSavingError
	}
	var parent interface{}
	if known {
		parent = current
	}
	versionInsertResult, versionSavingError := databaseTransaction.Exec("INSERT INTO file_versions (file_id, version_number, based_on_version, device_id, content_hash) VALUES (?, ?, ?, ?, ?)", metadata.FileID, next, parent, metadata.DeviceID, metadata.FileHash)
	if versionSavingError != nil {
		return versionSavingError
	}
	versionID, versionIDReadingError := versionInsertResult.LastInsertId()
	if versionIDReadingError != nil {
		return versionIDReadingError
	}
	for index, hash := range metadata.ChunkHashes {
		if _, chunkSavingError := databaseTransaction.Exec("INSERT INTO chunks (file_version_id, chunk_index, chunk_hash) VALUES (?, ?, ?)", versionID, index, hash); chunkSavingError != nil {
			return chunkSavingError
		}
	}
	return nil
}

// Rabindra Neupane
// Preserve incoming contents outside the watched folder and leave both the
// accepted file's metadata and its chunk cache unchanged. Repeated conflicts
// with identical contents reuse the same copy instead of multiplying files.
// Rabindra Neupane
// Save incoming conflict copies under Synced Files Conflict.
func (app *App) preserveIncomingConflict(metadata network.FileMetadata, verified string) error {
	folder := filepath.Join(filepath.Dir(app.syncFolderPath), "Synced Files Conflict")
	if conflictFolderCreationError := os.MkdirAll(folder, 0755); conflictFolderCreationError != nil {
		return conflictFolderCreationError
	}
	extension := filepath.Ext(metadata.FileName)
	fileName := strings.TrimSuffix(metadata.FileName, extension) + ".conflict-" + metadata.FileHash + extension
	destination := filepath.Join(folder, fileName)
	existingHash, conflictFileHashingError := hashStoredFile(destination)
	if conflictFileHashingError == nil {
		if existingHash != metadata.FileHash {
			return fmt.Errorf("existing conflict copy differs; preserve it before retrying")
		}
	} else if os.IsNotExist(conflictFileHashingError) {
		if conflictCopyingError := copyRemoteBackup(verified, destination); conflictCopyingError != nil {
			// copyRemoteBackup creates exclusively; never remove a preexisting file.
			return fmt.Errorf("preserve incoming conflict: %w", conflictCopyingError)
		}
	} else {
		return conflictFileHashingError
	}
	relative := filepath.Join("Synced Files Conflict", fileName)
	fmt.Printf("Synchronization conflict for %q: kept current file; incoming copy saved at %q\n", metadata.FileName, relative)
	return &network.SyncConflictError{ConflictFile: relative}
}

// Rabindra Neupane
var errVersionChunksDiffer = errors.New("version chunk contents differ")

// Rabindra Neupane
func compareRecordedChunks(databaseTransaction *sql.Tx, versionID int64, chunks []Chunk) error {
	chunkRows, chunkQueryError := databaseTransaction.Query("SELECT chunk_index, chunk_hash FROM chunks WHERE file_version_id = ? ORDER BY chunk_index", versionID)
	if chunkQueryError != nil {
		return chunkQueryError
	}
	defer chunkRows.Close()
	count := 0
	for chunkRows.Next() {
		var index int
		var hash string
		if chunkScanningError := chunkRows.Scan(&index, &hash); chunkScanningError != nil {
			return chunkScanningError
		}
		if count >= len(chunks) || index != count || hash != chunks[count].Hash {
			return fmt.Errorf("%w: chunk hashes differ", errVersionChunksDiffer)
		}
		count++
	}
	if chunkIterationError := chunkRows.Err(); chunkIterationError != nil {
		return chunkIterationError
	}
	if count != len(chunks) {
		return fmt.Errorf("%w: chunk counts differ", errVersionChunksDiffer)
	}
	return nil
}
