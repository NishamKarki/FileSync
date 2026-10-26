//////// Aurthor: Santosh Tripathee

package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Chunks a file, hashes it and records it as a new version in the database.
// If the file is new, its added to the files table with version 1.
// If it already exists, a new version is recoreded, linked back to the version it came
// from (this is what let us detect conflicts later).
//Rabindra Neupane
// Version management: Santosh Tripathee. Atomic recording and unchanged-content
// deduplication: Rabindra Neupane. Watcher events alone do not create revisions.
func RecordFileVersion(db *sql.DB, syncFolderPath, filePath, deviceID string, chunks []Chunk) error {
	if err := waitForFileReady(filePath); err != nil {
		return err
	}
	relativePath, err := filepath.Rel(syncFolderPath, filePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	err = tx.QueryRow("SELECT current_version FROM files WHERE id = ?", relativePath).Scan(&current)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	known := err == nil
	if known {
		var id int64
		if err := tx.QueryRow("SELECT id FROM file_versions WHERE file_id = ? AND version_number = ? ORDER BY id DESC LIMIT 1", relativePath, current).Scan(&id); err != nil {
			return err
		}
		comparison := compareRecordedChunks(tx, id, chunks)
		if comparison == nil {
			//Rabindra Neupane
			// Backfill Santosh's whole-file hash for pre-migration versions.
			if _, err := tx.Exec("UPDATE file_versions SET content_hash=? WHERE id=?", computeContentHash(chunks), id); err != nil {
				return err
			}
			return tx.Commit()
		}
		if !errors.Is(comparison, errVersionChunksDiffer) {
			return comparison
		}
	}
	var maximum sql.NullInt64
	if err := tx.QueryRow("SELECT MAX(version_number) FROM file_versions WHERE file_id = ?", relativePath).Scan(&maximum); err != nil {
		return err
	}
	next := current + 1
	if maximum.Valid && int64(next) <= maximum.Int64 {
		next = int(maximum.Int64) + 1
	}
	_, err = tx.Exec(`INSERT INTO files (id,path,name,current_version,size,modified_by)
        VALUES (?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
        current_version=excluded.current_version,size=excluded.size,
        modified_by=excluded.modified_by,updated_at=CURRENT_TIMESTAMP`, relativePath, relativePath, filepath.Base(filePath), next, info.Size(), deviceID)
	if err != nil {
		return err
	}
	var parent interface{}
	if known {
		parent = current
	}
	//Rabindra Neupane
	// Santosh's content hash joins the existing atomic version/chunk transaction.
	result, err := tx.Exec("INSERT INTO file_versions (file_id, version_number, based_on_version, device_id, content_hash) VALUES (?,?,?,?,?)", relativePath, next, parent, deviceID, computeContentHash(chunks))
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if _, err := tx.Exec("INSERT INTO chunks (file_version_id, chunk_index, chunk_hash) VALUES (?,?,?)", id, chunk.Index, chunk.Hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

//Rabindra Neupane
// An empty result is a known empty base; missing history is an explicit error.
func loadBaseChunkHashes(tx *sql.Tx, fileID string, version int) ([]string, error) {
	var id int64
	if err := tx.QueryRow("SELECT id FROM file_versions WHERE file_id = ? AND version_number = ? ORDER BY id DESC LIMIT 1", fileID, version).Scan(&id); err != nil {
		return nil, err
	}
	rows, err := tx.Query("SELECT chunk_index,chunk_hash FROM chunks WHERE file_version_id = ? ORDER BY chunk_index", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hashes := make([]string, 0)
	for rows.Next() {
		var index int
		var hash string
		if err := rows.Scan(&index, &hash); err != nil {
			return nil, err
		}
		if index != len(hashes) {
			return nil, fmt.Errorf("base version chunk indexes are incomplete")
		}
		hashes = append(hashes, hash)
	}
	return hashes, rows.Err()
}

//Rabindra Neupane
// Length-prefixed fixed-size hash lists identify file contents independently of
// local version counters. This is a revision identity, not the whole-file hash.
func chunkRevisionIdentity(size int64, hashes []string) string {
	hasher := sha256.New()
	fmt.Fprintf(hasher, "%d:%d:", size, len(hashes))
	for _, hash := range hashes {
		hasher.Write([]byte(hash))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

//Rabindra Neupane
func loadBaseRevisionHistory(tx *sql.Tx, fileID string, version int) ([]string, error) {
	identities := make([]string, 0)
	for version > 0 {
		hashes, err := loadBaseChunkHashes(tx, fileID, version)
		if err != nil {
			return nil, err
		}
		identities = append(identities, chunkRevisionIdentity(chunkSize, hashes))
		var parent sql.NullInt64
		if err := tx.QueryRow("SELECT based_on_version FROM file_versions WHERE file_id=? AND version_number=? ORDER BY id DESC LIMIT 1", fileID, version).Scan(&parent); err != nil {
			return nil, err
		}
		if !parent.Valid {
			break
		}
		if parent.Int64 < 1 || parent.Int64 >= int64(version) {
			return nil, fmt.Errorf("invalid version parent chain")
		}
		version = int(parent.Int64)
	}
	return identities, nil
}

// Some apps (like Word, or Windows during a large copy) briefly lock a file
// while writing it. Instead of failing immediately, wait a moment and retry
// a few times before giving up.
func waitForFileReady(filePath string) error {
	var lastError error

	for attempt := 0; attempt < 5; attempt++ {
		file, err := os.Open(filePath)
		if err == nil {
			file.Close()
			return nil
		}
		lastError = err
		time.Sleep(300 * time.Millisecond)
	}

	return lastError
}

// Author: Santosh Tripathee. Integration: Rabindra Neupane.
//Rabindra Neupane
// Calculates one SHA-256 hash for the whole file from its chunks.
// The chunks are fed in order, so the result is the same as hashing the
// complete file at once. The same content always gives the same hash, on any
// device and with any chunk size. Conflict detection compares these hashes
// instead of version numbers, because each device counts its own versions.
func computeContentHash(chunks []Chunk) string {
	contentHasher := sha256.New()

	// ChunkFile returns the chunks in order (index 0, 1, 2, ...)
	for _, chunk := range chunks {
		contentHasher.Write(chunk.Data)
	}

	return fmt.Sprintf("%x", contentHasher.Sum(nil))
}
