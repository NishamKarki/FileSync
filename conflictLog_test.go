// Author: Santosh Tripathee

package main

import (
	"path/filepath"
	"testing"
)

// Checks that conflicts are saved, listed, and not saved twice.
func TestRecordAndListConflicts(test *testing.T) {

	// A temporary database that is deleted when the test ends
	testDatabase, setupError := InitDatabase(filepath.Join(test.TempDir(), "Synced Files"))
	if setupError != nil {
		test.Fatal(setupError)
	}
	test.Cleanup(func() { testDatabase.Close() })

	// A new database has no conflicts, and returns an empty list (not nil)
	conflicts, listError := ListOpenConflicts(testDatabase)
	if listError != nil || conflicts == nil || len(conflicts) != 0 {
		test.Fatalf("expected an empty list, got %v (error: %v)", conflicts, listError)
	}

	firstConflict := ConflictRecord{
		FileID:         "notes.txt",
		LocalHash:      "hashB",
		IncomingHash:   "hashC",
		BaseHash:       "hashA",
		SenderDeviceID: "device-1",
		ConflictCopy:   "Synced Files Conflict/notes.conflict-hashC.txt",
	}

	// Recording the same conflict twice (a retried transfer) saves it once
	for attempt := 0; attempt < 2; attempt++ {
		if recordError := RecordConflict(testDatabase, firstConflict); recordError != nil {
			test.Fatal(recordError)
		}
	}
	conflicts, _ = ListOpenConflicts(testDatabase)
	if len(conflicts) != 1 {
		test.Fatalf("expected 1 conflict after a repeated record, got %d", len(conflicts))
	}

	// Every field comes back as it was saved
	saved := conflicts[0]
	if saved.FileID != "notes.txt" || saved.LocalHash != "hashB" || saved.IncomingHash != "hashC" ||
		saved.BaseHash != "hashA" || saved.SenderDeviceID != "device-1" ||
		saved.ConflictCopy != firstConflict.ConflictCopy || saved.DetectedAt == "" {
		test.Fatalf("saved conflict doesn't match: %+v", saved)
	}

	// A different incoming version of the same file is a separate conflict,
	// and the newest one is listed first
	secondConflict := firstConflict
	secondConflict.IncomingHash = "hashD"
	secondConflict.ConflictCopy = "Synced Files Conflict/notes.conflict-hashD.txt"
	if recordError := RecordConflict(testDatabase, secondConflict); recordError != nil {
		test.Fatal(recordError)
	}
	conflicts, _ = ListOpenConflicts(testDatabase)
	if len(conflicts) != 2 || conflicts[0].IncomingHash != "hashD" {
		test.Fatalf("expected 2 conflicts with the newest first, got %+v", conflicts)
	}

	// Once the user has resolved a conflict, the same two versions arriving
	// again are not recorded or shown again. (The resolve function comes in a
	// later step, so the test marks it resolved directly in the database.)
	_, resolveError := testDatabase.Exec(
		"UPDATE conflicts SET status = 'resolved' WHERE incoming_hash = 'hashC'",
	)
	if resolveError != nil {
		test.Fatal(resolveError)
	}
	if recordError := RecordConflict(testDatabase, firstConflict); recordError != nil {
		test.Fatal(recordError)
	}
	conflicts, _ = ListOpenConflicts(testDatabase)
	if len(conflicts) != 1 || conflicts[0].IncomingHash != "hashD" {
		test.Fatalf("a resolved conflict came back: %+v", conflicts)
	}
}
