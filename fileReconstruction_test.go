package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
