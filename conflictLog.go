// Author: Santosh Tripathee

package main

import (
	"database/sql"
	"fmt"
)

// ConflictRecord contains the conflict information shown in the frontend.
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

// RecordConflict saves a newly detected conflict.
// Repeated transfers of the same two versions do not create duplicate records.
func RecordConflict(database *sql.DB, conflict ConflictRecord) error {
	var existingConflictCount int
	conflictCheckError := database.QueryRow(
		`SELECT COUNT(*) FROM conflicts
		 WHERE file_id = ? AND local_hash = ? AND incoming_hash = ?`,
		conflict.FileID, conflict.LocalHash, conflict.IncomingHash,
	).Scan(&existingConflictCount)
	if conflictCheckError != nil {
		return conflictCheckError
	}
	if existingConflictCount > 0 {
		return nil
	}

	_, conflictInsertError := database.Exec(
		`INSERT INTO conflicts
		 (file_id, local_hash, incoming_hash, base_hash, sender_device_id, conflict_copy)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		conflict.FileID, conflict.LocalHash, conflict.IncomingHash,
		conflict.BaseHash, conflict.SenderDeviceID, conflict.ConflictCopy,
	)
	return conflictInsertError
}

// ListOpenConflicts returns unresolved conflicts with the newest one first.
func ListOpenConflicts(database *sql.DB) ([]ConflictRecord, error) {
	conflictRows, conflictQueryError := database.Query(
		`SELECT id, file_id, local_hash, incoming_hash,
		        COALESCE(base_hash, ''), COALESCE(sender_device_id, ''),
		        conflict_copy, strftime('%Y-%m-%dT%H:%M:%SZ', detected_at)
		 FROM conflicts
		 WHERE status = 'open'
		 ORDER BY id DESC`,
	)
	if conflictQueryError != nil {
		return nil, conflictQueryError
	}
	defer conflictRows.Close()

	openConflicts := make([]ConflictRecord, 0)
	for conflictRows.Next() {
		var conflict ConflictRecord
		conflictScanError := conflictRows.Scan(
			&conflict.ID, &conflict.FileID, &conflict.LocalHash, &conflict.IncomingHash,
			&conflict.BaseHash, &conflict.SenderDeviceID,
			&conflict.ConflictCopy, &conflict.DetectedAt,
		)
		if conflictScanError != nil {
			return nil, conflictScanError
		}
		openConflicts = append(openConflicts, conflict)
	}

	return openConflicts, conflictRows.Err()
}

// GetOpenConflict returns one conflict that has not already been resolved.
func GetOpenConflict(database *sql.DB, conflictID int64) (ConflictRecord, error) {
	var conflict ConflictRecord
	conflictQueryError := database.QueryRow(
		`SELECT id, file_id, local_hash, incoming_hash,
		        COALESCE(base_hash, ''), COALESCE(sender_device_id, ''),
		        conflict_copy, strftime('%Y-%m-%dT%H:%M:%SZ', detected_at)
		 FROM conflicts
		 WHERE id = ? AND status = 'open'`,
		conflictID,
	).Scan(
		&conflict.ID, &conflict.FileID, &conflict.LocalHash, &conflict.IncomingHash,
		&conflict.BaseHash, &conflict.SenderDeviceID,
		&conflict.ConflictCopy, &conflict.DetectedAt,
	)
	if conflictQueryError == sql.ErrNoRows {
		return conflict, fmt.Errorf("conflict %d was not found or is already resolved", conflictID)
	}
	return conflict, conflictQueryError
}

// MarkConflictResolved records the user's decision and advances the shared base
// to the incoming version that the user has now reviewed.
func MarkConflictResolved(database *sql.DB, conflict ConflictRecord, resolution string) error {
	databaseTransaction, transactionStartError := database.Begin()
	if transactionStartError != nil {
		return transactionStartError
	}
	defer databaseTransaction.Rollback()

	conflictUpdateResult, conflictUpdateError := databaseTransaction.Exec(
		`UPDATE conflicts
		 SET status = 'resolved', resolution = ?, resolved_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = 'open'`,
		resolution, conflict.ID,
	)
	if conflictUpdateError != nil {
		return conflictUpdateError
	}

	updatedConflictCount, affectedRowsError := conflictUpdateResult.RowsAffected()
	if affectedRowsError != nil {
		return affectedRowsError
	}
	if updatedConflictCount != 1 {
		return fmt.Errorf("conflict %d was not found or is already resolved", conflict.ID)
	}

	_, baseHashUpdateError := databaseTransaction.Exec(
		"UPDATE files SET last_synced_hash = ? WHERE id = ?",
		conflict.IncomingHash, conflict.FileID,
	)
	if baseHashUpdateError != nil {
		return baseHashUpdateError
	}

	return databaseTransaction.Commit()
}
