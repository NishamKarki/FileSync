// Author: Santosh Tripathee

package main

import (
	"FileSyncWails/network"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Checks every conflict rule with a simple example.
// The hashes are short labels like "hashA" instead of real SHA-256 values,
// because DetectConflict only checks whether hashes are equal.
func TestDetectConflict(t *testing.T) {

	testCases := []struct {
		name       string
		baseHash   string
		localHash  string
		remoteHash string
		expected   ConflictDecision
	}{
		{"both devices still have the base version", "hashA", "hashA", "hashA", InSync},
		{"both devices made the same edit", "hashA", "hashB", "hashB", InSync},
		{"only the other device edited", "hashA", "hashA", "hashB", AcceptRemote},
		{"only this device edited", "hashA", "hashB", "hashA", KeepLocal},
		{"both devices edited differently", "hashA", "hashB", "hashC", Conflict},
		{"file is new on this device", "", "", "hashA", AcceptRemote},
		{"created separately with different content", "", "hashA", "hashB", Conflict},
		{"created separately with the same content", "", "hashA", "hashA", InSync},
	}

	for _, testCase := range testCases {
		// t.Run gives each case its own PASS/FAIL line in the output
		t.Run(testCase.name, func(t *testing.T) {
			result := DetectConflict(testCase.baseHash, testCase.localHash, testCase.remoteHash)
			if result != testCase.expected {
				t.Errorf("got %s, expected %s", result, testCase.expected)
			}
		})
	}
}

//Rabindra Neupane
// Extend Santosh's rule tests with isolated network/database integration checks.
func conflictTestApp(t *testing.T, id string) (*App, string) {
	t.Helper()
	root := t.TempDir()
	syncPath := filepath.Join(root, "Synced Files")
	if err := os.MkdirAll(syncPath, 0755); err != nil {
		t.Fatal(err)
	}
	db, err := InitDatabase(syncPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Persist an already-assigned identity without requiring a real LAN coordinator.
	settings := network.DeviceSettings{RegistrationKey: id, DeviceID: id}
	data, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(root, ".filesync-device.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := network.OpenDeviceRegistry(context.Background(), db, root, 8080)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	return &App{db: db, syncFolderPath: syncPath, chunkStoragePath: filepath.Join(root, "File Chunks"), deviceRegistry: registry, ctx: context.Background()}, root
}

//Rabindra Neupane
func putConflictTestFile(t *testing.T, a *App, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(a.syncFolderPath, name), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

//Rabindra Neupane
func requireConflictTestContent(t *testing.T, a *App, name, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(a.syncFolderPath, name))
	if err != nil || string(data) != want {
		t.Fatalf("content got %q, %v; want %q", data, err, want)
	}
}

//Rabindra Neupane
func requireConflictTestBase(t *testing.T, a *App, name, want string) {
	t.Helper()
	got, err := GetBaseHash(a.db, name)
	if err != nil || got != want {
		t.Fatalf("base got %s, %v; want %s", got, err, want)
	}
}

//Rabindra Neupane
func conflictTestServer(t *testing.T, a *App, root string, drop *atomic.Bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/transfer/", network.NewTransferHandler(root, a.completeIncomingTransfer))
	mux.Handle("/file", network.NewWholeFileHandler(root, a.completeIncomingTransfer))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path == "/transfer/complete" || r.URL.Path == "/file") && drop != nil && drop.Swap(false) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, r)
			if recorder.Code != http.StatusOK {
				t.Errorf("receiver failed before lost acknowledgement: %s", recorder.Body.String())
			}
			http.Error(w, "simulated lost acknowledgement", http.StatusBadGateway)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

//Rabindra Neupane
func TestConflictNetworkIntegration(t *testing.T) {
	for _, whole := range []bool{false, true} {
		t.Run(fmt.Sprintf("whole=%v", whole), func(t *testing.T) {
			a, _ := conflictTestApp(t, strings.Repeat("a", 32))
			b, root := conflictTestApp(t, strings.Repeat("b", 32))
			server := conflictTestServer(t, b, root, nil)
			address := strings.TrimPrefix(server.URL, "http://")
			name := "report.txt"
			send := func() *SyncResult {
				t.Helper()
				result, err := a.sendPreparedFile(address, name, whole)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			putConflictTestFile(t, a, name, "base")
			if result := send(); !result.BaseUpdated || result.KeepLocal || result.ConflictFile != "" {
				t.Fatalf("initial result: %+v", result)
			}
			base, _ := HashFileOnDisk(filepath.Join(a.syncFolderPath, name))
			requireConflictTestBase(t, a, name, base)
			requireConflictTestBase(t, b, name, base)
			if result := send(); !result.BaseUpdated || (!whole && result.ChunksSent != 0) {
				t.Fatalf("in-sync result: %+v", result)
			}
			//Rabindra Neupane
			incremental, err := a.SyncFile(address, name)
			if err != nil || incremental.ChunksSent != 0 {
				t.Fatalf("verified cache reuse: %+v %v", incremental, err)
			}
			var versions int
			if err := b.db.QueryRow("SELECT COUNT(*) FROM file_versions WHERE file_id=?", name).Scan(&versions); err != nil || versions != 1 {
				t.Fatalf("duplicate received versions: %d, %v", versions, err)
			}
			putConflictTestFile(t, a, name, "new shared content")
			if result := send(); !result.BaseUpdated {
				t.Fatalf("accept-remote result: %+v", result)
			}
			requireConflictTestContent(t, b, name, "new shared content")
			base, _ = HashFileOnDisk(filepath.Join(a.syncFolderPath, name))
			// Database intentionally lags behind the receiver's actual edited file.
			putConflictTestFile(t, b, name, "receiver edit")
			if result := send(); !result.KeepLocal || result.BaseUpdated {
				t.Fatalf("keep-local result: %+v", result)
			}
			requireConflictTestContent(t, b, name, "receiver edit")
			requireConflictTestBase(t, a, name, base)
			requireConflictTestBase(t, b, name, base)
			putConflictTestFile(t, a, name, "sender edit")
			result := send()
			if result.ConflictFile == "" || result.BaseUpdated {
				t.Fatalf("conflict result: %+v", result)
			}
			conflictPath := filepath.Join(root, result.ConflictFile)
			data, err := os.ReadFile(conflictPath)
			if err != nil || string(data) != "sender edit" {
				t.Fatalf("preserved incoming copy: %q, %v", data, err)
			}
			requireConflictTestContent(t, b, name, "receiver edit")
			requireConflictTestBase(t, a, name, base)
			requireConflictTestBase(t, b, name, base)
			again := send()
			if again.ConflictFile != result.ConflictFile {
				t.Fatal("repeated conflict created another filename")
			}
			copies, err := os.ReadDir(filepath.Dir(conflictPath))
			if err != nil || len(copies) != 1 {
				t.Fatalf("conflict copies: %d, %v", len(copies), err)
			}
			// Verify received version hashes and persistent sender attribution.
			var sender, hash string
			err = b.db.QueryRow("SELECT device_id,content_hash FROM file_versions ORDER BY id DESC LIMIT 1").Scan(&sender, &hash)
			if err != nil || sender != strings.Repeat("a", 32) || hash != base {
				t.Fatalf("received metadata: %s %s, %v", sender, hash, err)
			}
		})
	}
}

//Rabindra Neupane
func TestLostAcknowledgementAndEmptyFiles(t *testing.T) {
	for _, whole := range []bool{false, true} {
		t.Run(fmt.Sprintf("whole=%v", whole), func(t *testing.T) {
			a, _ := conflictTestApp(t, strings.Repeat("a", 32))
			b, root := conflictTestApp(t, strings.Repeat("b", 32))
			var drop atomic.Bool
			server := conflictTestServer(t, b, root, &drop)
			address := strings.TrimPrefix(server.URL, "http://")
			name := "empty.txt"
			putConflictTestFile(t, a, name, "")
			drop.Store(true)
			if _, err := a.sendPreparedFile(address, name, whole); err == nil {
				t.Fatal("lost acknowledgement reported success")
			}
			requireConflictTestBase(t, a, name, "")
			hash, _ := HashFileOnDisk(filepath.Join(b.syncFolderPath, name))
			if hash == "" {
				t.Fatal("empty file mistaken for missing file")
			}
			requireConflictTestBase(t, b, name, hash)
			result, err := a.sendPreparedFile(address, name, whole)
			if err != nil || !result.BaseUpdated {
				t.Fatalf("retry: %+v, %v", result, err)
			}
			requireConflictTestBase(t, a, name, hash)
			for _, contents := range []string{strings.Repeat("binary\x00", 600), "short", ""} {
				putConflictTestFile(t, a, name, contents)
				result, err := a.sendPreparedFile(address, name, whole)
				if err != nil || !result.BaseUpdated {
					t.Fatalf("file change: %+v, %v", result, err)
				}
				requireConflictTestContent(t, b, name, contents)
			}
		})
	}
}

//Rabindra Neupane
func TestEditedSenderDoesNotAdvanceBase(t *testing.T) {
	a, _ := conflictTestApp(t, strings.Repeat("a", 32))
	name := "edited.txt"
	putConflictTestFile(t, a, name, "snapshot")
	a.fileProcessingMutex.Lock()
	_, metadata, err := a.prepareFileForSync(name)
	a.fileProcessingMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	putConflictTestFile(t, a, name, "later edit")
	result, err := a.finishOutgoingSync(name, metadata, &SyncResult{}, nil)
	if err != nil || result.BaseUpdated {
		t.Fatalf("edited sender advanced base: %+v, %v", result, err)
	}
	requireConflictTestBase(t, a, name, "")
	var recordedHash string
	if err := a.db.QueryRow("SELECT content_hash FROM file_versions WHERE file_id=?", name).Scan(&recordedHash); err != nil {
		t.Fatal(err)
	}
	if recordedHash != metadata.FileHash {
		t.Fatal("snapshot content hash not recorded")
	}
	if _, err := a.db.Exec("UPDATE file_versions SET content_hash=NULL WHERE file_id=?", name); err != nil {
		t.Fatal(err)
	}
	// A later preparation must backfill hashes even when chunk contents are unchanged.
	putConflictTestFile(t, a, name, "snapshot")
	a.fileProcessingMutex.Lock()
	_, _, err = a.prepareFileForSync(name)
	a.fileProcessingMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := GetLocalHash(a.db, name)
	if err != nil || actual != metadata.FileHash {
		t.Fatalf("legacy hash backfill: %s %v", actual, err)
	}
}

//Rabindra Neupane
func TestWholeFileCannotBypassConflictMetadata(t *testing.T) {
	a, _ := conflictTestApp(t, strings.Repeat("a", 32))
	b, root := conflictTestApp(t, strings.Repeat("b", 32))
	server := conflictTestServer(t, b, root, nil)
	address := strings.TrimPrefix(server.URL, "http://")
	name := "protected.txt"
	putConflictTestFile(t, a, name, "sender")
	putConflictTestFile(t, b, name, "receiver")
	a.fileProcessingMutex.Lock()
	_, metadata, err := a.prepareFileForSync(name)
	a.fileProcessingMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := network.SendFile(context.Background(), address, metadata, []byte("broken")); err == nil {
		t.Fatal("corrupt upload accepted")
	}
	requireConflictTestContent(t, b, name, "receiver")
	requireConflictTestBase(t, b, name, "")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("legacy unverified overwrite"))
	writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/file", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	network.NewWholeFileHandler(root, b.completeIncomingTransfer).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("legacy upload status %d", response.Code)
	}
	requireConflictTestContent(t, b, name, "receiver")
}

//Rabindra Neupane
func TestConflictSchemaMigrationPreservesVersions(t *testing.T) {
	a, _ := conflictTestApp(t, strings.Repeat("a", 32))
	name := "legacy.txt"
	putConflictTestFile(t, a, name, "existing content")
	a.fileProcessingMutex.Lock()
	_, metadata, err := a.prepareFileForSync(name)
	a.fileProcessingMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-Santosh schema while retaining the file/version rows.
	if _, err := a.db.Exec("ALTER TABLE files DROP COLUMN last_synced_hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("ALTER TABLE file_versions DROP COLUMN content_hash"); err != nil {
		t.Fatal(err)
	}
	db, err := InitDatabase(a.syncFolderPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM file_versions WHERE file_id=?", name).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy versions lost: %d %v", count, err)
	}
	hash, err := GetLocalHash(db, name)
	if err != nil || hash != "" {
		t.Fatalf("legacy content hash should be unknown: %q %v", hash, err)
	}
	if err := SetBaseHash(db, name, metadata.FileHash); err != nil {
		t.Fatal(err)
	}
	base, err := GetBaseHash(db, name)
	if err != nil || base != metadata.FileHash {
		t.Fatalf("migrated base: %s %v", base, err)
	}
}
