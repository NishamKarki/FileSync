package main

import (
	"os"
	"path/filepath"
	"testing"
)

//Rabindra Neupane
func TestSelectExistingSyncedFile(t *testing.T) {
	for _, name := range []string{"received.txt", "received (1).txt", "empty.txt"} {
		t.Run(name, func(t *testing.T) {
			app := incomingTestApp(t)
			content := "received contents"
			if name == "empty.txt" {
				content = ""
			}
			path := filepath.Join(app.syncFolderPath, name)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			if selected := app.addSelectedFile(path); selected != name {
				t.Fatalf("selected %q, want %q", selected, name)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatal("selection changed the synced file")
			}
			entries, err := os.ReadDir(app.syncFolderPath)
			if err != nil || len(entries) != 1 {
				t.Fatal("selection created a duplicate file")
			}
		})
	}
}

//Rabindra Neupane
func TestAddExternalFileSelectsMatchingCopy(t *testing.T) {
	app := incomingTestApp(t)
	external := filepath.Join(t.TempDir(), "received.txt")
	if err := os.WriteFile(external, []byte("same contents"), 0644); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if selected := app.addSelectedFile(external); selected != "received.txt" {
			t.Fatalf("selected %q", selected)
		}
	}
	entries, err := os.ReadDir(app.syncFolderPath)
	if err != nil || len(entries) != 1 {
		t.Fatal("reselection created a duplicate")
	}
	// Different contents still receive a separate name, preserving Nisham's behavior.
	if err := os.WriteFile(external, []byte("different contents"), 0644); err != nil {
		t.Fatal(err)
	}
	if selected := app.addSelectedFile(external); selected != "received (1).txt" {
		t.Fatalf("selected %q", selected)
	}
	original, err := os.ReadFile(filepath.Join(app.syncFolderPath, "received.txt"))
	if err != nil || string(original) != "same contents" {
		t.Fatal("name collision overwrote original")
	}
}
