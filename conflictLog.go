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
	// Check whether the same local and incoming versions were already recorded.
	var existingConflictCount int
	conflictCheckError := database.QueryRow(
		`SELECT COUNT(*) FROM conflicts
		 WHERE file_id = ? AND local_hash = ? AND incoming_hash = ?`,
		conflict.FileID, conflict.LocalHash, conflict.IncomingHash,
	).Scan(&existingConflictCount)
	// Return when the duplicate check cannot be completed.
	if conflictCheckError != nil {
		return conflictCheckError
	}
	// Skip inserting a duplicate conflict from a retried transfer.
	if existingConflictCount > 0 {
		return nil
	}

	// Insert the new unresolved conflict and its preserved copy path.
	_, conflictInsertError := database.Exec(
		`INSERT INTO conflicts
		 (file_id, local_hash, incoming_hash, base_hash, sender_device_id, conflict_copy)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		conflict.FileID, conflict.LocalHash, conflict.IncomingHash,
		conflict.BaseHash, conflict.SenderDeviceID, conflict.ConflictCopy,
	)
	// Return the database insertion result.
	return conflictInsertError
}

// ListOpenConflicts returns unresolved conflicts with the newest one first.
func ListOpenConflicts(database *sql.DB) ([]ConflictRecord, error) {
	// Query unresolved conflicts with the newest record first.
	conflictRows, conflictQueryError := database.Query(
		`SELECT id, file_id, local_hash, incoming_hash,
		        COALESCE(base_hash, ''), COALESCE(sender_device_id, ''),
		        conflict_copy, strftime('%Y-%m-%dT%H:%M:%SZ', detected_at)
		 FROM conflicts
		 WHERE status = 'open'
		 ORDER BY id DESC`,
	)
	// Return when the conflict query cannot be started.
	if conflictQueryError != nil {
		return nil, conflictQueryError
	}
	// Close the database rows after the list is built.
	defer conflictRows.Close()

	// Read every open conflict into a frontend-safe record.
	openConflicts := make([]ConflictRecord, 0)
	for conflictRows.Next() {
		// Scan the current database row into one conflict record.
		var conflict ConflictRecord
		conflictScanError := conflictRows.Scan(
			&conflict.ID, &conflict.FileID, &conflict.LocalHash, &conflict.IncomingHash,
			&conflict.BaseHash, &conflict.SenderDeviceID,
			&conflict.ConflictCopy, &conflict.DetectedAt,
		)
		// Stop when a row cannot be decoded correctly.
		if conflictScanError != nil {
			return nil, conflictScanError
		}
		// Add the valid conflict to the result list.
		openConflicts = append(openConflicts, conflict)
	}

	// Return the list and any final row iteration error.
	return openConflicts, conflictRows.Err()
}

// GetOpenConflict returns one conflict that has not already been resolved.
func GetOpenConflict(database *sql.DB, conflictID int64) (ConflictRecord, error) {
	// Query the requested conflict only while it remains unresolved.
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
	// Return a clear message when the conflict is missing or already resolved.
	if conflictQueryError == sql.ErrNoRows {
		return conflict, fmt.Errorf("conflict %d was not found or is already resolved", conflictID)
	}
	// Return the conflict and any other database error.
	return conflict, conflictQueryError
}

// MarkConflictResolved records the user's decision and advances the shared base
// to the incoming version that the user has now reviewed.
func MarkConflictResolved(database *sql.DB, conflict ConflictRecord, resolution string) error {
	// Start one transaction for the decision and shared-base update.
	databaseTransaction, transactionStartError := database.Begin()
	if transactionStartError != nil {
		return transactionStartError
	}
	// Roll back automatically if any later database operation fails.
	defer databaseTransaction.Rollback()

	// Mark the open conflict with the user's resolution choice.
	conflictUpdateResult, conflictUpdateError := databaseTransaction.Exec(
		`UPDATE conflicts
		 SET status = 'resolved', resolution = ?, resolved_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = 'open'`,
		resolution, conflict.ID,
	)
	// Stop when the conflict record cannot be updated.
	if conflictUpdateError != nil {
		return conflictUpdateError
	}

	// Confirm that exactly one open conflict was changed.
	updatedConflictCount, affectedRowsError := conflictUpdateResult.RowsAffected()
	if affectedRowsError != nil {
		return affectedRowsError
	}
	// Reject a repeated or stale resolution request.
	if updatedConflictCount != 1 {
		return fmt.Errorf("conflict %d was not found or is already resolved", conflict.ID)
	}

	// Advance the shared base to the incoming version reviewed by the user.
	_, baseHashUpdateError := databaseTransaction.Exec(
		"UPDATE files SET last_synced_hash = ? WHERE id = ?",
		conflict.IncomingHash, conflict.FileID,
	)
	// Stop when the shared base cannot be saved.
	if baseHashUpdateError != nil {
		return baseHashUpdateError
	}

	// Commit both database changes together.
	return databaseTransaction.Commit()
}
