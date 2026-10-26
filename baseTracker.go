// Author: Santosh Tripathee

package main

import (
	"database/sql"
	"os"
)

//Rabindra Neupane
// Implement Santosh's agreed disk-hash contract until his helper is available.
// Missing files differ from empty files (whose SHA-256 is nonempty).
func HashFileOnDisk(path string) (string, error) {
	hash, err := hashStoredFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return hash, err
}

// Returns the hash both devices last agreed on for this file.
// Returns "" if the file has never been synced, or isn't in the database.
func GetBaseHash(db *sql.DB, fileID string) (string, error) {

	// NullString handles NULL, which means "never synced"
	var baseHash sql.NullString
	queryError := db.QueryRow(
		"SELECT last_synced_hash FROM files WHERE id = ?", fileID,
	).Scan(&baseHash)

	if queryError == sql.ErrNoRows {
		return "", nil
	}
	if queryError != nil {
		return "", queryError
	}

	// A NULL value becomes "" here
	return baseHash.String, nil
}

// Returns this device's newest content hash for the file.
// Returns "" if the file doesn't exist on this device (never added, or deleted).
func GetLocalHash(db *sql.DB, fileID string) (string, error) {

	// Joining with files means deleted files return nothing, because deleting
	// a file removes its row from files but keeps its old versions.
	// The newest version is the one with the highest id.
	var localHash sql.NullString
	queryError := db.QueryRow(
		`SELECT file_versions.content_hash
		 FROM file_versions
		 JOIN files ON files.id = file_versions.file_id
		 WHERE file_versions.file_id = ?
		 ORDER BY file_versions.id DESC
		 LIMIT 1`,
		fileID,
	).Scan(&localHash)

	if queryError == sql.ErrNoRows {
		return "", nil
	}
	if queryError != nil {
		return "", queryError
	}

	return localHash.String, nil
}

// Saves the hash both devices now agree on, after a successful sync.
// The sender calls this after sending a file, and the receiver after accepting it.
func SetBaseHash(db *sql.DB, fileID string, agreedHash string) error {

	_, updateError := db.Exec(
		"UPDATE files SET last_synced_hash = ? WHERE id = ?",
		agreedHash, fileID,
	)
	return updateError
}
