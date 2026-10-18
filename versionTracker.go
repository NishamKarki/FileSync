//////// Aurthor: Santosh Tripathee

package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"
)

// Chunks a file, hashes it and records it as a new version in the database.
// If the file is new, its added to the files table with version 1.
// If it already exists, a new version is recoreded, linked back to the version it came
// from (this is what let us detect conflicts later).
func RecordFileVersion(db *sql.DB,
	syncFolderPath string,
	filePath string,
	deviceID string,
	chunks []Chunk) error {

	// Wait for the file to be fully written/unlocked before trying to read it
	if readyError := waitForFileReady(filePath); readyError != nil {
		return readyError
	}

	// Use the file's path relatives to the sync folder as its unique ID
	relativePath, err := filepath.Rel(syncFolderPath, filePath)
	if err != nil {
		return err
	}
	fileName := filepath.Base(filePath)

	// Get the file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	fileSize := fileInfo.Size()

	// check if this file already exists in the database
	var currentVersion int
	lookupError := db.QueryRow(
		"SELECT current_version FROM files WHERE id = ?", relativePath,
	).Scan(&currentVersion)

	var newVersionNumber int
	var basedOnVersion interface{}

	if lookupError == sql.ErrNoRows {
		// Brand new file — this is version 1
		newVersionNumber = 1
		basedOnVersion = nil

		_, insertError := db.Exec(
			`INSERT INTO files (id, path, name, current_version, size, modified_by, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			relativePath, relativePath, fileName, newVersionNumber, fileSize, deviceID,
		)
		if insertError != nil {
			return insertError
		}

	} else if lookupError != nil {
		return lookupError

	} else {
		// File already exists — this is a new version based on the current one
		newVersionNumber = currentVersion + 1
		basedOnVersion = currentVersion

		_, updateError := db.Exec(
			`UPDATE files SET current_version = ?, size = ?, modified_by = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ?`,
			newVersionNumber, fileSize, deviceID, relativePath,
		)
		if updateError != nil {
			return updateError
		}
	}

	// Record this version in file_versions
	result, versionInsertError := db.Exec(
		`INSERT INTO file_versions (file_id, version_number, based_on_version, device_id)
		 VALUES (?, ?, ?, ?)`,
		relativePath, newVersionNumber, basedOnVersion, deviceID,
	)
	if versionInsertError != nil {
		return versionInsertError
	}

	fileVersionID, idError := result.LastInsertId()
	if idError != nil {
		return idError
	}

	// Record each chunk, linked to this version
	for _, chunk := range chunks {
		_, chunkInsertError := db.Exec(
			`INSERT INTO chunks (file_version_id, chunk_index, chunk_hash)
			 VALUES (?, ?, ?)`,
			fileVersionID, chunk.Index, chunk.Hash,
		)
		if chunkInsertError != nil {
			return chunkInsertError
		}
	}

	return nil
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
