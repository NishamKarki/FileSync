package main

import (
	"FileSyncWails/network"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

//Rabindra Neupane
func hashStoredFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

//Rabindra Neupane
// Caller holds fileProcessingMutex. Keep the marker while contents match so
// delayed/duplicate watcher events are ignored. A real edit removes the marker.
func (a *App) matchesRemoteFile(path string) bool {
	path = filepath.Clean(path)
	expected, exists := a.remoteFileHashes[path]
	if !exists {
		return false
	}
	actual, err := hashStoredFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			delete(a.remoteFileHashes, path)
		}
		return false
	}
	if actual == expected {
		return true
	}
	delete(a.remoteFileHashes, path)
	return false
}

//Rabindra Neupane
// Reconstruction: Nisham Karki. Version schema: Santosh Tripathee.
// Remote application and watcher coordination: Rabindra Neupane.
func (a *App) applyIncomingTransfer(metadata network.FileMetadata, chunkRoot string) error {
	a.fileProcessingMutex.Lock()
	defer a.fileProcessingMutex.Unlock()
	if a.db == nil || a.syncFolderPath == "" {
		return fmt.Errorf("receiver storage or metadata database is unavailable")
	}
	if metadata.FileID != metadata.FileName || metadata.FileName == "" || filepath.Base(metadata.FileName) != metadata.FileName || metadata.Version < 1 {
		return fmt.Errorf("invalid received file identity or version")
	}
	// Reconstruct and verify outside Synced Files. Local reconstruction continues
	// to use Reconstructed Files through the existing public App method.
	outputRoot, err := os.MkdirTemp(chunkRoot, "verified-output-")
	if err != nil {
		return err
	}
	cleanupOutput := true
	defer func() {
		if cleanupOutput {
			os.RemoveAll(outputRoot)
		}
	}()
	transferApp := &App{chunkStoragePath: chunkRoot, reconstructedFilesPath: outputRoot}
	if err := transferApp.reconstructFile(metadata.FileName, &metadata); err != nil {
		return err
	}
	verified := filepath.Join(outputRoot, metadata.FileName)
	if err := os.MkdirAll(a.syncFolderPath, 0755); err != nil {
		return err
	}
	destination := filepath.Join(a.syncFolderPath, metadata.FileName)
	previousHash, previousErr := hashStoredFile(destination)
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return previousErr
	}
	hadPrevious := previousErr == nil
	// Preserve a rollback copy outside the watched folder before any replacement.
	backupRoot, err := os.MkdirTemp(outputRoot, "backup-")
	if err != nil {
		return err
	}
	backup := filepath.Join(backupRoot, "previous-file")
	if hadPrevious {
		if err := copyRemoteBackup(destination, backup); err != nil {
			return err
		}
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := a.recordReceivedVersion(tx, metadata, destination, hadPrevious); err != nil {
		return err
	}
	// Recheck after metadata work so a user edit during verification isn't lost.
	currentHash, currentErr := hashStoredFile(destination)
	if (hadPrevious && (currentErr != nil || currentHash != previousHash)) || (!hadPrevious && !os.IsNotExist(currentErr)) {
		return fmt.Errorf("destination changed during transfer; retry after local processing")
	}
	if err := replaceVerifiedFile(verified, destination); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		// File and SQLite cannot share one atomic transaction. Restore the prior
		// file if the database commit fails, and report any rollback failure.
		var restoreErr error
		if hadPrevious {
			restoreErr = replaceVerifiedFile(backup, destination)
		} else {
			restoreErr = os.Remove(destination)
		}
		if restoreErr != nil {
			cleanupOutput = false
			return fmt.Errorf("metadata commit failed: %v; file rollback failed: %v; recovery files retained at %s", err, restoreErr, outputRoot)
		}
		if hadPrevious {
			if a.remoteFileHashes == nil {
				a.remoteFileHashes = make(map[string]string)
			}
			a.remoteFileHashes[filepath.Clean(destination)] = previousHash
		}
		return fmt.Errorf("received metadata commit failed: %w", err)
	}
	if a.remoteFileHashes == nil {
		a.remoteFileHashes = make(map[string]string)
	}
	a.remoteFileHashes[filepath.Clean(destination)] = metadata.FileHash
	fmt.Printf("Received and verified %s in Synced Files (version %d)\n", metadata.FileName, metadata.Version)
	return nil
}

//Rabindra Neupane
func copyRemoteBackup(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

//Rabindra Neupane
// Preserve the sender's version/base and hashes using Santosh's existing tables.
// Unknown sender device IDs stay NULL. Do not label a received update as a new
// local version, downgrade newer versions, or overwrite unprocessed local edits.
func (a *App) recordReceivedVersion(tx *sql.Tx, m network.FileMetadata, destination string, hadPrevious bool) error {
	var current int
	err := tx.QueryRow("SELECT current_version FROM files WHERE id = ?", m.FileID).Scan(&current)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	known := err == nil
	if known && current > m.Version {
		return fmt.Errorf("receiver has a newer file version")
	}
	if hadPrevious {
		if !known {
			hash, err := hashStoredFile(destination)
			if err != nil {
				return err
			}
			if hash != m.FileHash {
				return fmt.Errorf("destination has untracked content; preserve or rename it before syncing")
			}
		} else {
			chunks, err := a.ChunkFile(destination)
			if err != nil {
				return err
			}
			var versionID int64
			if err := tx.QueryRow("SELECT id FROM file_versions WHERE file_id = ? AND version_number = ? ORDER BY id DESC LIMIT 1", m.FileID, current).Scan(&versionID); err != nil {
				return err
			}
			if err := compareRecordedChunks(tx, versionID, chunks); err != nil {
				return fmt.Errorf("destination has a local edit awaiting processing: %w", err)
			}
		}
	}
	var versionID int64
	var base sql.NullInt64
	err = tx.QueryRow("SELECT id, based_on_version FROM file_versions WHERE file_id = ? AND version_number = ? ORDER BY id DESC LIMIT 1", m.FileID, m.Version).Scan(&versionID, &base)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	versionExists := err == nil
	if versionExists {
		if (m.BaseVersion == nil) != !base.Valid || (m.BaseVersion != nil && int64(*m.BaseVersion) != base.Int64) {
			return fmt.Errorf("received version conflicts with recorded base version")
		}
		chunks := make([]Chunk, len(m.ChunkHashes))
		for index, hash := range m.ChunkHashes {
			chunks[index] = Chunk{Index: index, Hash: hash}
		}
		if err := compareRecordedChunks(tx, versionID, chunks); err != nil {
			return fmt.Errorf("received version conflicts with recorded chunk hashes")
		}
	}
	_, err = tx.Exec(`INSERT INTO files (id, path, name, current_version, size, modified_by)
		VALUES (?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET current_version=excluded.current_version,
		size=excluded.size, modified_by=NULL, updated_at=CURRENT_TIMESTAMP`, m.FileID, m.FileID, m.FileName, m.Version, m.FileSize)
	if err != nil {
		return err
	}
	if !versionExists {
		var basedOn interface{}
		if m.BaseVersion != nil {
			basedOn = *m.BaseVersion
		}
		result, err := tx.Exec("INSERT INTO file_versions (file_id, version_number, based_on_version, device_id) VALUES (?, ?, ?, NULL)", m.FileID, m.Version, basedOn)
		if err != nil {
			return err
		}
		versionID, err = result.LastInsertId()
		if err != nil {
			return err
		}
		for index, hash := range m.ChunkHashes {
			if _, err := tx.Exec("INSERT INTO chunks (file_version_id, chunk_index, chunk_hash) VALUES (?, ?, ?)", versionID, index, hash); err != nil {
				return err
			}
		}
	}
	return nil
}

//Rabindra Neupane
func compareRecordedChunks(tx *sql.Tx, versionID int64, chunks []Chunk) error {
	rows, err := tx.Query("SELECT chunk_index, chunk_hash FROM chunks WHERE file_version_id = ? ORDER BY chunk_index", versionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var index int
		var hash string
		if err := rows.Scan(&index, &hash); err != nil {
			return err
		}
		if count >= len(chunks) || index != count || hash != chunks[count].Hash {
			return fmt.Errorf("chunk hashes differ")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(chunks) {
		return fmt.Errorf("chunk counts differ")
	}
	return nil
}
