package main

import (
	"os"
	"path/filepath"
	"testing"
)

//Rabindra Neupane
// Santosh's version recorder must advance for content changes, not duplicate events.
func TestLocalVersionDeduplicationAndRollback(t *testing.T) {
	app := incomingTestApp(t)
	path := filepath.Join(app.syncFolderPath, "local.txt")
	if err := os.WriteFile(path, []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	chunks, err := app.ChunkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := RecordFileVersion(app.db, app.syncFolderPath, path, "local", chunks); err != nil {
			t.Fatal(err)
		}
	}
	var count, current int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM file_versions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate events produced %d versions", count)
	}
	if _, err := app.db.Exec(`CREATE TRIGGER reject_new_chunks BEFORE INSERT ON chunks BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	chunks, err = app.ChunkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordFileVersion(app.db, app.syncFolderPath, path, "local", chunks); err == nil {
		t.Fatal("expected failed chunk insert")
	}
	if err := app.db.QueryRow("SELECT current_version FROM files WHERE id='local.txt'").Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := app.db.QueryRow("SELECT COUNT(*) FROM file_versions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if current != 1 || count != 1 {
		t.Fatal("failed recording left a partial version")
	}
}

//Rabindra Neupane
func TestReceivedUpdatesUseContentBaseAcrossLocalCounters(t *testing.T) {
	for _, localCount := range []int{2, 7} {
		t.Run(string(rune('0'+localCount)), func(t *testing.T) {
			app := incomingTestApp(t)
			path := filepath.Join(app.syncFolderPath, "received.txt")
			for index := 0; index < localCount; index++ {
				data := string(rune('a' + index))
				if index == localCount-1 {
					data = "shared base"
				}
				if err := os.WriteFile(path, []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
				chunks, err := app.ChunkFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := RecordFileVersion(app.db, app.syncFolderPath, path, "local", chunks); err != nil {
					t.Fatal(err)
				}
			}
			metadata, root := incomingTestMetadata(t, "incoming update", 2, "shared base")
			if err := app.completeIncomingTransfer(metadata, root); err != nil {
				t.Fatal(err)
			}
			chunks, err := app.ChunkFile(path)
			if err != nil {
				t.Fatal(err)
			}
			recorded, err := app.metadataForSync("received.txt", chunks)
			if err != nil {
				t.Fatal(err)
			}
			if recorded.Version != localCount+1 || recorded.BaseVersion == nil || *recorded.BaseVersion != localCount {
				t.Fatalf("sender counter replaced local history: %+v", recorded)
			}
			if len(recorded.BaseChunkHashes) != 1 || recorded.BaseChunkHashes[0] != metadata.BaseChunkHashes[0] {
				t.Fatal("local parent does not identify shared base")
			}
			if err := app.completeIncomingTransfer(metadata, root); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := app.db.QueryRow("SELECT COUNT(*) FROM file_versions").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != localCount+1 {
				t.Fatal("repeat receive created duplicate revision")
			}
		})
	}
}
