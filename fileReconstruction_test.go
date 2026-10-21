package main

import (
	"FileSyncWails/network"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

//Rabindra Neupane
// Reconstruction implementation: Nisham Karki
// Transfer verification tests: Rabindra Neupane
func TestVerifiedIncomingReconstruction(t *testing.T) {
	for _, name := range []string{"valid", "bad final hash", "wrong size", "empty file", "wrong chunk count"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			chunkRoot := filepath.Join(root, "incoming")
			folder := filepath.Join(chunkRoot, "test.txt")
			//Rabindra Neupane
			outputRoot := filepath.Join(root, "Synced Files")
			for _, directory := range []string{folder, outputRoot} {
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			content := "abcdef"
			if name == "empty file" {
				content = ""
			}
			hash := sha256.Sum256([]byte(content))
			//Rabindra Neupane
			base := 1
			metadata := network.FileMetadata{FileID: "test.txt", FileName: "test.txt", Version: 2, BaseVersion: &base, ChunkSize: chunkSize, FileSize: int64(len(content)), FileHash: hex.EncodeToString(hash[:])}
			//Rabindra Neupane
			previousHash := sha256.Sum256([]byte("previous"))
			metadata.BaseChunkHashes = []string{hex.EncodeToString(previousHash[:])}
			if content != "" {
				//Rabindra Neupane
				metadata.ChunkHashes = []string{hex.EncodeToString(hash[:])}
				if err := os.WriteFile(filepath.Join(folder, "chunk_0.chunk"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if name == "bad final hash" {
				metadata.FileHash = "incorrect"
			}
			if name == "wrong size" {
				metadata.FileSize++
			}
			if name == "wrong chunk count" {
				metadata.ChunkHashes = append(metadata.ChunkHashes, "missing")
			}
			output := filepath.Join(outputRoot, "test.txt")
			if err := os.WriteFile(output, []byte("previous"), 0644); err != nil {
				t.Fatal(err)
			}
			//Rabindra Neupane
			db, err := InitDatabase(outputRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			app := &App{syncFolderPath: outputRoot, db: db}
			previousChunks, err := app.ChunkFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if err := RecordFileVersion(db, outputRoot, output, "test-device", previousChunks); err != nil {
				t.Fatal(err)
			}
			err = app.completeIncomingTransfer(metadata, chunkRoot)
			valid := name == "valid" || name == "empty file"
			if valid && err != nil {
				t.Fatal(err)
			}
			if !valid && err == nil {
				t.Fatal("expected verification failure")
			}
			stored, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			want := content
			if !valid {
				want = "previous"
			}
			if string(stored) != want {
				t.Fatalf("output=%q, want=%q", stored, want)
			}
		})
	}
}

//Rabindra Neupane
func TestAppReconstructFileFromReceivedChunks(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		name := "complete"
		if incomplete {
			name = "missing middle chunk"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			app := &App{
				chunkStoragePath:       filepath.Join(root, "File Chunks"),
				reconstructedFilesPath: filepath.Join(root, "Reconstructed Files"),
			}
			folder := filepath.Join(app.chunkStoragePath, "test.txt")
			for _, path := range []string{folder, app.reconstructedFilesPath} {
				if err := os.MkdirAll(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			chunks := map[string]string{"chunk_0.chunk": "abcd", "chunk_1.chunk": "ef"}
			if incomplete {
				delete(chunks, "chunk_1.chunk")
				chunks["chunk_2.chunk"] = "ef"
			}
			for name, data := range chunks {
				if err := os.WriteFile(filepath.Join(folder, name), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(app.reconstructedFilesPath, "test.txt")
			if err := os.WriteFile(output, []byte("previous"), 0644); err != nil {
				t.Fatal(err)
			}
			err := app.ReconstructFile("test.txt")
			if incomplete && err == nil {
				t.Fatal("expected missing chunk error")
			}
			if !incomplete && err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			want := "abcdef"
			if incomplete {
				want = "previous"
			}
			if string(data) != want {
				t.Fatalf("got %q, want %q", data, want)
			}
		})
	}
}
