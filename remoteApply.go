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
	//Rabindra Neupane
	// Compare actual bytes to the sender's base, never independent counters.
	if hadPrevious && previousHash != metadata.FileHash {
		matchesBase := false
		if metadata.ChunkSize == chunkSize {
			chunks, err := a.ChunkFile(destination)
			if err != nil {
				return err
			}
			matchesBase = metadata.BaseChunkHashes != nil && len(chunks) == len(metadata.BaseChunkHashes)
			if matchesBase {
				for index, chunk := range chunks {
					if chunk.Hash != metadata.BaseChunkHashes[index] {
						matchesBase = false
						break
					}
				}
			}
			//Rabindra Neupane
			if !matchesBase && len(metadata.BaseRevisions) > 0 {
				hashes := make([]string, len(chunks))
				for index, chunk := range chunks {
					hashes[index] = chunk.Hash
				}
				identity := chunkRevisionIdentity(chunkSize, hashes)
				for _, ancestor := range metadata.BaseRevisions {
					if ancestor == identity {
						matchesBase = true
						break
					}
				}
			}
		}
		if !matchesBase {
			return a.preserveIncomingConflict(metadata, verified)
		}
	}
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
	if err := a.recordReceivedVersion(tx, metadata); err != nil {
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
	//Rabindra Neupane
	fmt.Printf("Received and verified %s in Synced Files (sender version %d; recorded with receiver's local sequence)\n", metadata.FileName, metadata.Version)
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
		//Rabindra Neupane
		os.Remove(destination) // Exclusively created by this call; never a preexisting copy.
		return copyErr
	}
	//Rabindra Neupane
	if closeErr != nil {
		os.Remove(destination)
	}
	return closeErr
}

//Rabindra Neupane
// Version schema: Santosh Tripathee. Receiver mapping: Rabindra Neupane.
// Sender counters are not shared revision IDs. Accepted contents get a local
// sequence number and local parent; repeated identical contents reuse the row.
func (a *App) recordReceivedVersion(tx *sql.Tx, m network.FileMetadata) error {
	var current int
	err := tx.QueryRow("SELECT current_version FROM files WHERE id = ?", m.FileID).Scan(&current)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	known := err == nil
	if known {
		var id int64
		if err := tx.QueryRow("SELECT id FROM file_versions WHERE file_id = ? AND version_number = ? ORDER BY id DESC LIMIT 1", m.FileID, current).Scan(&id); err != nil {
			return err
		}
		incoming := make([]Chunk, len(m.ChunkHashes))
		for index, hash := range m.ChunkHashes {
			incoming[index] = Chunk{Index: index, Hash: hash}
		}
		comparison := compareRecordedChunks(tx, id, incoming)
		if comparison == nil {
			_, err := tx.Exec("UPDATE files SET size = ? WHERE id = ?", m.FileSize, m.FileID)
			return err
		}
		if !errors.Is(comparison, errVersionChunksDiffer) {
			return comparison
		}
	}
	var maximum sql.NullInt64
	if err := tx.QueryRow("SELECT MAX(version_number) FROM file_versions WHERE file_id = ?", m.FileID).Scan(&maximum); err != nil {
		return err
	}
	next := current + 1
	if maximum.Valid && int64(next) <= maximum.Int64 {
		next = int(maximum.Int64) + 1
	}
	_, err = tx.Exec(`INSERT INTO files (id, path, name, current_version, size, modified_by)
        VALUES (?, ?, ?, ?, ?, NULL)
        ON CONFLICT(id) DO UPDATE SET current_version=excluded.current_version,
        size=excluded.size, modified_by=NULL, updated_at=CURRENT_TIMESTAMP`, m.FileID, m.FileID, m.FileName, next, m.FileSize)
	if err != nil {
		return err
	}
	var parent interface{}
	if known {
		parent = current
	}
	result, err := tx.Exec("INSERT INTO file_versions (file_id, version_number, based_on_version, device_id) VALUES (?, ?, ?, NULL)", m.FileID, next, parent)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for index, hash := range m.ChunkHashes {
		if _, err := tx.Exec("INSERT INTO chunks (file_version_id, chunk_index, chunk_hash) VALUES (?, ?, ?)", id, index, hash); err != nil {
			return err
		}
	}
	return nil
}

//Rabindra Neupane
// Preserve incoming contents outside the watched folder and leave both the
// accepted file's metadata and its chunk cache unchanged. Repeated conflicts
// with identical contents reuse the same copy instead of multiplying files.
func (a *App) preserveIncomingConflict(metadata network.FileMetadata, verified string) error {
	folder := filepath.Join(filepath.Dir(a.syncFolderPath), "Conflicting Files")
	if err := os.MkdirAll(folder, 0755); err != nil {
		return err
	}
	extension := filepath.Ext(metadata.FileName)
	fileName := strings.TrimSuffix(metadata.FileName, extension) + ".conflict-" + metadata.FileHash + extension
	destination := filepath.Join(folder, fileName)
	existingHash, err := hashStoredFile(destination)
	if err == nil {
		if existingHash != metadata.FileHash {
			return fmt.Errorf("existing conflict copy differs; preserve it before retrying")
		}
	} else if os.IsNotExist(err) {
		if err := copyRemoteBackup(verified, destination); err != nil {
			// copyRemoteBackup creates exclusively; never remove a preexisting file.
			return fmt.Errorf("preserve incoming conflict: %w", err)
		}
	} else {
		return err
	}
	relative := filepath.Join("Conflicting Files", fileName)
	fmt.Printf("Synchronization conflict for %q: kept current file; incoming copy saved at %q\n", metadata.FileName, relative)
	return &network.SyncConflictError{ConflictFile: relative}
}

//Rabindra Neupane
var errVersionChunksDiffer = errors.New("version chunk contents differ")

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
			return fmt.Errorf("%w: chunk hashes differ", errVersionChunksDiffer)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(chunks) {
		return fmt.Errorf("%w: chunk counts differ", errVersionChunksDiffer)
	}
	return nil
}
