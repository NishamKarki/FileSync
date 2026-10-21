package network

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func metadataHash(data string) string {
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func testFileMetadata() FileMetadata {
	return FileMetadata{
		FileID: "sample.txt", FileName: "sample.txt", Version: 1,
		FileSize: 6, ChunkSize: 4,
		ChunkHashes: []string{metadataHash("abcd"), metadataHash("ef")},
		FileHash:    metadataHash("abcdef"),
	}
}

func TestCompareFileMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks map[string]string
		needed []int
	}{
		{"initial sync", nil, []int{0, 1}},
		{"unchanged", map[string]string{"chunk_0.chunk": "abcd", "chunk_1.chunk": "ef"}, []int{}},
		{"changed", map[string]string{"chunk_0.chunk": "abXd", "chunk_1.chunk": "ef"}, []int{0}},
		{"missing", map[string]string{"chunk_0.chunk": "abcd"}, []int{1}},
		{"truncated", map[string]string{"chunk_0.chunk": "abc", "chunk_1.chunk": "ef"}, []int{0}},
		{"oversized", map[string]string{"chunk_0.chunk": "abcdX", "chunk_1.chunk": "ef"}, []int{0}},
		{"old trailing chunk", map[string]string{"chunk_0.chunk": "abcd", "chunk_1.chunk": "ef", "chunk_2.chunk": "old"}, []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			folder := filepath.Join(root, "File Chunks", "sample.txt")
			if err := os.MkdirAll(folder, 0755); err != nil {
				t.Fatal(err)
			}
			for name, data := range tc.chunks {
				if err := os.WriteFile(filepath.Join(folder, name), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(testFileMetadata())
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			compareFileMetadata(response, httptest.NewRequest(http.MethodPost, "/file-metadata", bytes.NewReader(body)), root)
			if response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			var result FileMetadataComparison
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.NeededChunks, tc.needed) {
				t.Fatalf("needed %v, want %v", result.NeededChunks, tc.needed)
			}
			// Negotiation must not overwrite or remove any stored chunk.
			for name, data := range tc.chunks {
				stored, err := os.ReadFile(filepath.Join(folder, name))
				if err != nil || string(stored) != data {
					t.Fatalf("stored chunk changed: %s", name)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "Synced Files")); !os.IsNotExist(err) {
				t.Fatal("comparison created destination files")
			}
		})
	}
}

func TestValidateFileMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*FileMetadata)
	}{
		{"path traversal", func(m *FileMetadata) { m.FileName = "../sample.txt"; m.FileID = m.FileName }},
		{"windows path", func(m *FileMetadata) { m.FileName = `..\sample.txt`; m.FileID = m.FileName }},
		{"different ID", func(m *FileMetadata) { m.FileID = "other.txt" }},
		{"negative size", func(m *FileMetadata) { m.FileSize = -1 }},
		{"zero chunk size", func(m *FileMetadata) { m.ChunkSize = 0 }},
		{"wrong chunk count", func(m *FileMetadata) { m.ChunkHashes = m.ChunkHashes[:1] }},
		{"bad chunk hash", func(m *FileMetadata) { m.ChunkHashes[0] = "bad" }},
		{"bad file hash", func(m *FileMetadata) { m.FileHash = "bad" }},
		{"zero version", func(m *FileMetadata) { m.Version = 0 }},
		{"invalid base", func(m *FileMetadata) { v := 1; m.BaseVersion = &v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testFileMetadata()
			tc.edit(&m)
			body, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			compareFileMetadata(response, httptest.NewRequest(http.MethodPost, "/file-metadata", bytes.NewReader(body)), t.TempDir())
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestFileMetadataRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		status             int
	}{
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"malformed JSON", http.MethodPost, "{", http.StatusBadRequest},
		{"unknown field", http.MethodPost, `{"unexpected":true}`, http.StatusBadRequest},
		{"trailing JSON", http.MethodPost, `{} {}`, http.StatusBadRequest},
		{"empty file", http.MethodPost, `{"fileId":"empty.txt","fileName":"empty.txt","version":1,"fileSize":0,"chunkSize":1024,"chunkHashes":[],"fileHash":"` + metadataHash("") + `"}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			compareFileMetadata(response, httptest.NewRequest(tc.method, "/file-metadata", bytes.NewBufferString(tc.body)), t.TempDir())
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == http.StatusOK && response.Body.String() != "{\"neededChunks\":[]}\n" {
				t.Fatal(response.Body.String())
			}
		})
	}
}
