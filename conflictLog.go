// Author: Santosh Tripathee

package main

import (
	"database/sql"
)

// One detected conflict, as the conflict window will show it.
// The json names are what the frontend (TypeScript) will see.
type ConflictRecord struct {
	ID             int64  `json:"id"`
	FileID         string `json:"fileId"`
	LocalHash      string `json:"localHash"`
	IncomingHash   string `json:"incomingHash"`
	BaseHash       string `json:"baseHash"`
	SenderDeviceID string `json:"senderDeviceId"`
	ConflictCopy   string `json:"conflictCopy"`
	DetectedAt     string `json:"detectedAt"`
}

// Saves a newly detected conflict so the user can be told about it.
// If this exact conflict (same file, same two versions) was recorded before,
// it is not saved again, whether it is still open or already resolved:
//   - still open: the user already knows about it (a transfer was retried)
//   - resolved: the user already decided, so we don't ask again
func RecordConflict(database *sql.DB, conflict ConflictRecord) error {

	// Has this exact conflict been recorded before?
	var existingCount int
	checkError := database.QueryRow(
		`SELECT COUNT(*) FROM conflicts
		 WHERE file_id = ? AND local_hash = ? AND incoming_hash = ?`,
		conflict.FileID, conflict.LocalHash, conflict.IncomingHash,
	).Scan(&existingCount)
	if checkError != nil {
		return checkError
	}
	if existingCount > 0 {
		return nil
	}

	// New conflict: save it. detected_at and status fill in automatically.
	_, insertError := database.Exec(
		`INSERT INTO conflicts
		 (file_id, local_hash, incoming_hash, base_hash, sender_device_id, conflict_copy)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		conflict.FileID, conflict.LocalHash, conflict.IncomingHash,
		conflict.BaseHash, conflict.SenderDeviceID, conflict.ConflictCopy,
	)
	return insertError
}

// Returns the conflicts the user hasn't resolved yet, newest first.
func ListOpenConflicts(database *sql.DB) ([]ConflictRecord, error) {

	// COALESCE turns NULL into "", because a Go string can't hold NULL.
	// strftime writes the time in a standard format ending in Z (UTC),
	// so the screen can convert it to the user's local time.
	rows, queryError := database.Query(
		`SELECT id, file_id, local_hash, incoming_hash,
		        COALESCE(base_hash, ''), COALESCE(sender_device_id, ''),
		        conflict_copy, strftime('%Y-%m-%dT%H:%M:%SZ', detected_at)
		 FROM conflicts
		 WHERE status = 'open'
		 ORDER BY id DESC`,
	)
	if queryError != nil {
		return nil, queryError
	}
	defer rows.Close()

	// Start with an empty list (not nil), so the screen gets [] when there
	// are no conflicts instead of null.
	openConflicts := make([]ConflictRecord, 0)

	for rows.Next() {
		var conflict ConflictRecord
		scanError := rows.Scan(
			&conflict.ID, &conflict.FileID, &conflict.LocalHash, &conflict.IncomingHash,
			&conflict.BaseHash, &conflict.SenderDeviceID,
			&conflict.ConflictCopy, &conflict.DetectedAt,
		)
		if scanError != nil {
			return nil, scanError
		}
		openConflicts = append(openConflicts, conflict)
	}

	return openConflicts, rows.Err()
}
