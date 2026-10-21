package main

import (
	"FileSyncWails/network"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//Rabindra Neupane
// Exercises the real sender, HTTP receiver, SQLite versions, and Nisham's
// reconstruction together. All files and databases live in temporary folders.
func TestSyncFileEndToEnd(t *testing.T) {
	sourceRoot := t.TempDir()
	receiverRoot := t.TempDir()
	source := &App{ctx: context.Background(), syncFolderPath: filepath.Join(sourceRoot, "Synced Files")}
	//Rabindra Neupane
	receiver := &App{syncFolderPath: filepath.Join(receiverRoot, "Synced Files"), reconstructedFilesPath: filepath.Join(receiverRoot, "Reconstructed Files")}
	for _, path := range []string{source.syncFolderPath, receiver.syncFolderPath, receiver.reconstructedFilesPath} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := InitDatabase(source.syncFolderPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source.db = db
	//Rabindra Neupane
	receiverDB, err := InitDatabase(receiver.syncFolderPath)
	if err != nil {
		t.Fatal(err)
	}
	defer receiverDB.Close()
	receiver.db = receiverDB
	handler := network.NewTransferHandler(receiverRoot, receiver.completeIncomingTransfer)
	uploads := 0
	completionFailure := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/transfer/chunk" {
			uploads++
		}
		if completionFailure && r.URL.Path == "/transfer/complete" {
			http.Error(w, "interrupted", 503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	fileName := "sync-test.txt"
	path := filepath.Join(source.syncFolderPath, fileName)
	content := bytes.Repeat([]byte("a"), 2*chunkSize+17)
	for _, step := range []struct {
		name   string
		change func()
		wanted int
		fail   bool
	}{
		{"initial", func() {}, 3, false},
		{"unchanged", func() {}, 0, false},
		{"one byte edit", func() { content[chunkSize+5] = 'X' }, 1, false},
		{"truncate", func() { content = content[:chunkSize+9] }, 1, false},
		{"empty", func() { content = nil }, 0, false},
		{"completion failure", func() { content = []byte("new version"); completionFailure = true }, 1, true},
	} {
		t.Run(step.name, func(t *testing.T) {
			step.change()
			if err := os.WriteFile(path, content, 0644); err != nil {
				t.Fatal(err)
			}
			chunks, err := source.ChunkFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := RecordFileVersion(db, source.syncFolderPath, path, "test-device", chunks); err != nil {
				t.Fatal(err)
			}
			before := uploads
			result, err := source.SyncFile(address, fileName)
			if uploads-before != step.wanted {
				t.Fatalf("uploaded %d chunks, want %d", uploads-before, step.wanted)
			}
			if step.fail {
				if err == nil || result != nil {
					t.Fatal("reported success despite completion failure")
				}
				//Rabindra Neupane
				stored, readErr := os.ReadFile(filepath.Join(receiver.syncFolderPath, fileName))
				if readErr != nil || len(stored) != 0 {
					t.Fatal("failed completion replaced previous empty file")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.ChunksSent != step.wanted {
				t.Fatalf("unexpected result: %+v", result)
			}
			//Rabindra Neupane
			stored, err := os.ReadFile(filepath.Join(receiver.syncFolderPath, fileName))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stored, content) {
				t.Fatal("receiver content differs")
			}
			//Rabindra Neupane
			receivedChunks, err := receiver.ChunkFile(filepath.Join(receiver.syncFolderPath, fileName))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := receiver.metadataForSync(fileName, receivedChunks); err != nil {
				t.Fatalf("received file cannot be synced back: %v", err)
			}
			if result.ChunksSent == 0 && result.ChunkBytesSent != 0 {
				t.Fatal("unchanged sync counted chunk bytes")
			}
			if step.name == "one byte edit" && result.ChunkBytesSent != chunkSize {
				t.Fatalf("expected one chunk payload, got %d", result.ChunkBytesSent)
			}
		})
	}
}

//Rabindra Neupane
func TestSyncFileRejectsStaleVersionAndOverlap(t *testing.T) {
	root := t.TempDir()
	app := &App{syncFolderPath: root}
	app.transferMutex.Lock()
	if _, err := app.SyncFile("unused", "test.txt"); err == nil {
		t.Fatal("accepted overlapping sync")
	}
	app.transferMutex.Unlock()
	if _, err := app.SyncFile("unused", "../test.txt"); err == nil {
		t.Fatal("accepted path")
	}
	db, err := InitDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app.db = db
	path := filepath.Join(root, "test.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	chunks, err := app.ChunkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SyncFile("unused", "test.txt"); err == nil || !strings.Contains(err.Error(), "processed") {
		t.Fatalf("missing version: %v", err)
	}
	if err := RecordFileVersion(db, root, path, "device", chunks); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SyncFile("unused", "test.txt"); err == nil || !strings.Contains(err.Error(), "processed") {
		t.Fatalf("stale version: %v", err)
	}
}

//Rabindra Neupane
func TestSyncFileSendsRecordedVersion(t *testing.T) {
	root := t.TempDir()
	app := &App{syncFolderPath: root}
	db, err := InitDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app.db = db
	path := filepath.Join(root, "test.txt")
	if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	chunks, err := app.ChunkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := RecordFileVersion(db, root, path, "device", chunks); err != nil {
			t.Fatal(err)
		}
	}
	metadata, err := app.metadataForSync("test.txt", chunks)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Version != 2 || metadata.BaseVersion == nil || *metadata.BaseVersion != 1 {
		t.Fatalf("wrong version: %+v", metadata)
	}
}

//Rabindra Neupane
func TestSyncFileRejectsInvalidReceiverPlan(t *testing.T) {
	// Validate receiver indexes before indexing sender chunks or uploading data.
	for _, body := range []string{`{"transferId":"id","neededChunks":[-1]}`, `{"transferId":"id","neededChunks":[1]}`, `{"transferId":"id","neededChunks":[0,0]}`} {
		t.Run(fmt.Sprint(body), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
			defer server.Close()
			metadata := network.FileMetadata{FileID: "file.txt", FileName: "file.txt", Version: 1, FileSize: 1, ChunkSize: 1024, ChunkHashes: []string{strings.Repeat("a", 64)}, FileHash: strings.Repeat("a", 64)}
			if _, err := network.StartChunkTransfer(context.Background(), strings.TrimPrefix(server.URL, "http://"), metadata); err == nil {
				t.Fatal("accepted invalid plan")
			}
		})
	}
}
