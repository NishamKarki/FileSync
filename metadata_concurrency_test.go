package main

import (
	"path/filepath"
	"testing"
	"time"
)

// TestBaseHashWaitsForInventoryWrite verifies that a shared-base update waits
// for another local SQLite write instead of failing with SQLITE_BUSY.
func TestBaseHashWaitsForInventoryWrite(t *testing.T) {
	// Create an isolated database that uses the normal FileSync setup.
	database, databaseOpeningError := InitDatabase(filepath.Join(t.TempDir(), "Synced Files"))
	if databaseOpeningError != nil {
		t.Fatalf("open metadata database: %v", databaseOpeningError)
	}
	t.Cleanup(func() { database.Close() })

	// Create the file row that the shared-base update will modify.
	if _, fileSavingError := database.Exec("INSERT INTO files(id, path, name, current_version) VALUES(?, ?, ?, ?)", "test.bin", "test.bin", "test.bin", 1); fileSavingError != nil {
		t.Fatalf("create file metadata: %v", fileSavingError)
	}

	// Hold a write transaction to model a chunk-inventory replacement.
	inventoryTransaction, transactionStartError := database.Begin()
	if transactionStartError != nil {
		t.Fatalf("start inventory transaction: %v", transactionStartError)
	}
	if _, inventoryWriteError := inventoryTransaction.Exec("UPDATE files SET modified_by=? WHERE id=?", "inventory", "test.bin"); inventoryWriteError != nil {
		inventoryTransaction.Rollback()
		t.Fatalf("hold inventory write lock: %v", inventoryWriteError)
	}

	// Start the base update while the inventory write owns SQLite's writer slot.
	baseUpdateResult := make(chan error, 1)
	go func() {
		baseUpdateResult <- SetBaseHash(database, "test.bin", "verified-hash")
	}()

	// Confirm the update waits rather than returning SQLITE_BUSY immediately.
	select {
	case baseUpdateError := <-baseUpdateResult:
		inventoryTransaction.Rollback()
		t.Fatalf("base update returned before inventory write finished: %v", baseUpdateError)
	case <-time.After(100 * time.Millisecond):
	}

	// Release the inventory write and require the queued base update to succeed.
	if transactionCommitError := inventoryTransaction.Commit(); transactionCommitError != nil {
		t.Fatalf("commit inventory transaction: %v", transactionCommitError)
	}
	select {
	case baseUpdateError := <-baseUpdateResult:
		if baseUpdateError != nil {
			t.Fatalf("save shared base after inventory write: %v", baseUpdateError)
		}
	case <-time.After(time.Second):
		t.Fatal("base update did not finish after inventory write released the database")
	}
}
