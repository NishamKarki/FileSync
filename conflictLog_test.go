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

// Checks that resolving a conflict saves the decision and the new base
// together, and that the same conflict can't be resolved twice.
func TestResolveConflictSavesDecisionAndBase(test *testing.T) {

	testDatabase, setupError := InitDatabase(filepath.Join(test.TempDir(), "Synced Files"))
	if setupError != nil {
		test.Fatal(setupError)
	}
	test.Cleanup(func() { testDatabase.Close() })

	// The file the conflict is about, as it would already be in the files table
	_, fileError := testDatabase.Exec(
		"INSERT INTO files (id, path, name, last_synced_hash) VALUES ('notes.txt', 'notes.txt', 'notes.txt', 'hashA')",
	)
	if fileError != nil {
		test.Fatal(fileError)
	}

	recordError := RecordConflict(testDatabase, ConflictRecord{
		FileID:       "notes.txt",
		LocalHash:    "hashB",
		IncomingHash: "hashC",
		BaseHash:     "hashA",
		ConflictCopy: "Synced Files Conflict/notes.conflict-hashC.txt",
	})
	if recordError != nil {
		test.Fatal(recordError)
	}
	openConflicts, _ := ListOpenConflicts(testDatabase)
	conflictID := openConflicts[0].ID

	// An open conflict can be loaded by its ID
	conflict, loadError := GetOpenConflict(testDatabase, conflictID)
	if loadError != nil || conflict.IncomingHash != "hashC" {
		test.Fatalf("could not load the open conflict: %+v (error: %v)", conflict, loadError)
	}

	// Resolve it
	if resolveError := MarkConflictResolved(testDatabase, conflict, "keep-mine"); resolveError != nil {
		test.Fatal(resolveError)
	}

	// The decision and its time were saved
	var resolution string
	var resolvedAtSaved bool
	checkError := testDatabase.QueryRow(
		"SELECT resolution, resolved_at IS NOT NULL FROM conflicts WHERE id = ?", conflictID,
	).Scan(&resolution, &resolvedAtSaved)
	if checkError != nil || resolution != "keep-mine" || !resolvedAtSaved {
		test.Fatalf("decision not saved: %q, time saved: %v (error: %v)", resolution, resolvedAtSaved, checkError)
	}

	// The base moved from hashA to the incoming version, hashC
	baseHash, baseError := GetBaseHash(testDatabase, "notes.txt")
	if baseError != nil || baseHash != "hashC" {
		test.Fatalf("expected base hashC, got %q (error: %v)", baseHash, baseError)
	}

	// It is no longer in the open list
	openConflicts, _ = ListOpenConflicts(testDatabase)
	if len(openConflicts) != 0 {
		test.Fatalf("resolved conflict is still listed: %+v", openConflicts)
	}

	// A second resolve (for example, a double click) is refused,
	// and the conflict can no longer be loaded as open
	if MarkConflictResolved(testDatabase, conflict, "use-incoming") == nil {
		test.Fatal("the same conflict was resolved twice")
	}
	if _, secondLoadError := GetOpenConflict(testDatabase, conflictID); secondLoadError == nil {
		test.Fatal("a resolved conflict was loaded as open")
	}
}
