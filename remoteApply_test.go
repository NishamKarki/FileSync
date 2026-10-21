package main

import (
	"FileSyncWails/network"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

//Rabindra Neupane
func incomingTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	app := &App{syncFolderPath: filepath.Join(root, "Synced Files"), reconstructedFilesPath: filepath.Join(root, "Reconstructed Files"), chunkStoragePath: filepath.Join(root, "File Chunks")}
	for _, path := range []string{app.syncFolderPath, app.reconstructedFilesPath, app.chunkStoragePath} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := InitDatabase(app.syncFolderPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	app.db = db
	return app
}

//Rabindra Neupane
//Rabindra Neupane
func incomingTestMetadata(t *testing.T, data string, version int, baseData ...string) (network.FileMetadata, string) {
	t.Helper()
	root := t.TempDir()
	folder := filepath.Join(root, "received.txt")
	if err := os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(data))
	metadata := network.FileMetadata{FileID: "received.txt", FileName: "received.txt", Version: version, ChunkSize: chunkSize, FileSize: int64(len(data)), FileHash: hex.EncodeToString(hash[:]), ChunkHashes: []string{}}
	if data != "" {
		metadata.ChunkHashes = []string{metadata.FileHash}
		if err := os.WriteFile(filepath.Join(folder, "chunk_0.chunk"), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if version > 1 {
		base := version - 1
		metadata.BaseVersion = &base
	}
	//Rabindra Neupane
	if len(baseData) > 0 {
		metadata.BaseChunkHashes = []string{}
		if baseData[0] != "" {
			hash := sha256.Sum256([]byte(baseData[0]))
			metadata.BaseChunkHashes = []string{hex.EncodeToString(hash[:])}
		}
	}
	return metadata, root
}

//Rabindra Neupane
func TestReceivedFileWatcherSuppression(t *testing.T) {
	app := incomingTestApp(t)
	metadata, root := incomingTestMetadata(t, "remote contents", 1)
	if err := app.completeIncomingTransfer(metadata, root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(app.syncFolderPath, metadata.FileName)
	for _, event := range []fsnotify.Op{fsnotify.Create, fsnotify.Write, fsnotify.Remove, fsnotify.Rename, fsnotify.Create | fsnotify.Write} {
		// Runtime context is deliberately absent: remote events must return
		// before local notifications, metadata deletion, or timers are triggered.
		app.HandleFileEvent(fsnotify.Event{Name: path, Op: event})
	}
	if len(app.fileTimers) != 0 {
		t.Fatal("remote events scheduled local processing")
	}
	// A timer queued before receipt must also recognize the remote bytes.
	app.ScheduleFileProcessing(path)
	time.Sleep(time.Second)
	var count int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM file_versions WHERE file_id = ?", metadata.FileID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate versions: %d", count)
	}
	if _, err := os.Stat(filepath.Join(app.reconstructedFilesPath, metadata.FileName)); !os.IsNotExist(err) {
		t.Fatal("remote timer ran local reconstruction")
	}
	if err := os.WriteFile(path, []byte("local change"), 0644); err != nil {
		t.Fatal(err)
	}
	app.fileProcessingMutex.Lock()
	ignored := app.matchesRemoteFile(path)
	app.fileProcessingMutex.Unlock()
	if ignored {
		t.Fatal("later local edit was ignored")
	}
	// The ordinary local version recorder can now advance from the received base.
	chunks, err := app.ChunkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordFileVersion(app.db, app.syncFolderPath, path, "local-device", chunks); err != nil {
		t.Fatal(err)
	}
	result, err := app.metadataForSync(metadata.FileName, chunks)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != 2 || result.BaseVersion == nil || *result.BaseVersion != 1 {
		t.Fatalf("wrong local successor: %+v", result)
	}
}

//Rabindra Neupane
func TestReceivedVersionIdempotencyAndConflicts(t *testing.T) {
	app := incomingTestApp(t)
	metadata, root := incomingTestMetadata(t, "remote contents", 1)
	if err := app.completeIncomingTransfer(metadata, root); err != nil {
		t.Fatal(err)
	}
	if err := app.completeIncomingTransfer(metadata, root); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM file_versions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("repeat created a duplicate version")
	}
	conflict, conflictRoot := incomingTestMetadata(t, "different contents", 1)
	if err := app.completeIncomingTransfer(conflict, conflictRoot); err == nil {
		t.Fatal("accepted conflicting version")
	}
	path := filepath.Join(app.syncFolderPath, metadata.FileName)
	if err := os.WriteFile(path, []byte("pending local edit"), 0644); err != nil {
		t.Fatal(err)
	}
	next, nextRoot := incomingTestMetadata(t, "next remote version", 2)
	if err := app.completeIncomingTransfer(next, nextRoot); err == nil {
		t.Fatal("overwrote pending local edit")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "pending local edit" {
		t.Fatal("local edit was not preserved")
	}
}

//Rabindra Neupane
func TestIncomingMetadataFailurePreservesDestination(t *testing.T) {
	app := incomingTestApp(t)
	metadata, root := incomingTestMetadata(t, "original", 1)
	if err := app.completeIncomingTransfer(metadata, root); err != nil {
		t.Fatal(err)
	}
	_, err := app.db.Exec(`CREATE TRIGGER reject_received_version BEFORE INSERT ON file_versions
		BEGIN SELECT RAISE(ABORT, 'test metadata failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	//Rabindra Neupane
	next, nextRoot := incomingTestMetadata(t, "new remote", 2, "original")
	if err := app.completeIncomingTransfer(next, nextRoot); err == nil {
		t.Fatal("expected metadata failure")
	}
	data, err := os.ReadFile(filepath.Join(app.syncFolderPath, metadata.FileName))
	if err != nil || string(data) != "original" {
		t.Fatal("metadata failure changed destination")
	}
	var current int
	if err := app.db.QueryRow("SELECT current_version FROM files WHERE id=?", metadata.FileID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 1 {
		t.Fatal("metadata transaction did not roll back")
	}
}

//Rabindra Neupane
func TestReplacementFailurePreservesDestination(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	if err := os.WriteFile(destination, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := replaceVerifiedFile(filepath.Join(root, "missing-source"), destination); err == nil {
		t.Fatal("expected rename failure")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "original" {
		t.Fatal("failed replacement lost original file")
	}
}

//Rabindra Neupane
func TestIncomingCommitFailureRestoresPreviousFile(t *testing.T) {
	app := incomingTestApp(t)
	app.db.SetMaxOpenConns(1)
	metadata, root := incomingTestMetadata(t, "original", 1)
	if err := app.completeIncomingTransfer(metadata, root); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"CREATE TABLE fail_commit (file_id TEXT REFERENCES files(id) DEFERRABLE INITIALLY DEFERRED)",
		`CREATE TRIGGER fail_received_commit AFTER INSERT ON file_versions
		BEGIN INSERT INTO fail_commit VALUES ('missing-parent'); END`,
	} {
		if _, err := app.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	//Rabindra Neupane
	next, nextRoot := incomingTestMetadata(t, "replacement", 2, "original")
	if err := app.completeIncomingTransfer(next, nextRoot); err == nil {
		t.Fatal("expected commit failure")
	}
	data, err := os.ReadFile(filepath.Join(app.syncFolderPath, metadata.FileName))
	if err != nil || string(data) != "original" {
		t.Fatal("commit failure did not restore previous file")
	}
	var current int
	if err := app.db.QueryRow("SELECT current_version FROM files WHERE id=?", metadata.FileID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 1 {
		t.Fatal("commit failure left received metadata active")
	}
}
