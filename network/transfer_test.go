package network

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

//Rabindra Neupane
func TestIsolatedChunkTransfer(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "File Chunks", "sample.txt")
	if err := os.MkdirAll(cache, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"chunk_0.chunk": "abcd", "chunk_1.chunk": "OLD", "chunk_2.chunk": "stale"} {
		if err := os.WriteFile(filepath.Join(cache, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	receiver := newTransferReceiver(root, func(metadata FileMetadata, chunkRoot string) error {
		calls++
		if chunkRoot == filepath.Join(root, "File Chunks") {
			t.Fatal("reconstruction used shared cache")
		}
		entries, err := os.ReadDir(filepath.Join(chunkRoot, metadata.FileName))
		if err != nil {
			return err
		}
		if len(entries) != 2 {
			t.Fatalf("stale chunks included: %v", entries)
		}
		var content []byte
		for index := range metadata.ChunkHashes {
			chunk, err := readVerifiedChunk(filepath.Join(chunkRoot, metadata.FileName, fmt.Sprintf("chunk_%d.chunk", index)), metadata, index)
			if err != nil {
				return err
			}
			content = append(content, chunk...)
		}
		if string(content) != "abcdef" {
			t.Fatalf("wrong file: %q", content)
		}
		return nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/transfer/start", receiver.start)
	mux.HandleFunc("/transfer/chunk", receiver.chunk)
	mux.HandleFunc("/transfer/complete", receiver.complete)
	server := httptest.NewServer(mux)
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	ctx := context.Background()
	plan, err := StartChunkTransfer(ctx, address, testFileMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.NeededChunks, []int{1}) {
		t.Fatal(plan.NeededChunks)
	}
	if err := CompleteChunkTransfer(ctx, address, plan.TransferID); err == nil {
		t.Fatal("completed with missing chunk")
	}
	if calls != 0 {
		t.Fatal("reconstructed before all chunks arrived")
	}
	if err := SendTransferChunk(ctx, address, plan.TransferID, 1, []byte("XX")); err == nil {
		t.Fatal("accepted corrupt chunk")
	}
	if err := SendTransferChunk(ctx, address, plan.TransferID, 2, []byte("ef")); err == nil {
		t.Fatal("accepted invalid index")
	}
	if err := SendTransferChunk(ctx, address, plan.TransferID, 1, []byte("ef")); err != nil {
		t.Fatal(err)
	}
	// Duplicate uploads before completion are safe and must not reconstruct.
	if err := SendTransferChunk(ctx, address, plan.TransferID, 1, []byte("ef")); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(cache, "chunk_1.chunk"))
	if err != nil || string(old) != "OLD" {
		t.Fatal("upload overwrote cache")
	}
	if calls != 0 {
		t.Fatal("upload triggered premature reconstruction")
	}
	// Altering the shared cache cannot change this transfer's snapshot.
	if err := os.WriteFile(filepath.Join(cache, "chunk_0.chunk"), []byte("XXXX"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CompleteChunkTransfer(ctx, address, plan.TransferID); err != nil {
		t.Fatal(err)
	}
	if err := CompleteChunkTransfer(ctx, address, plan.TransferID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("completion called reconstruction %d times", calls)
	}
	if err := SendTransferChunk(ctx, address, plan.TransferID, 1, []byte("ef")); err == nil {
		t.Fatal("accepted upload after completion")
	}
	second, err := StartChunkTransfer(ctx, address, testFileMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if len(second.NeededChunks) != 0 {
		t.Fatalf("verified chunks not cached: %v", second.NeededChunks)
	}
	// A second transfer gets its own folder and ID.
	if second.TransferID == plan.TransferID || receiver.transfers[second.TransferID].root == receiver.transfers[plan.TransferID].root {
		t.Fatal("transfers share identity/storage")
	}
}

//Rabindra Neupane
func TestTransferCompletionFailures(t *testing.T) {
	for _, tc := range []string{"reconstruction error", "corrupt staged chunk", "expired", "empty file"} {
		t.Run(tc, func(t *testing.T) {
			root := t.TempDir()
			calls := 0
			receiver := newTransferReceiver(root, func(metadata FileMetadata, chunkRoot string) error {
				calls++
				if tc == "reconstruction error" {
					return fmt.Errorf("verification failed")
				}
				return nil
			})
			mux := http.NewServeMux()
			mux.HandleFunc("/transfer/start", receiver.start)
			mux.HandleFunc("/transfer/chunk", receiver.chunk)
			mux.HandleFunc("/transfer/complete", receiver.complete)
			server := httptest.NewServer(mux)
			defer server.Close()
			address := strings.TrimPrefix(server.URL, "http://")
			metadata := testFileMetadata()
			if tc == "empty file" {
				metadata.FileSize = 0
				metadata.ChunkHashes = []string{}
				metadata.FileHash = metadataHash("")
			}
			ctx := context.Background()
			plan, err := StartChunkTransfer(ctx, address, metadata)
			if err != nil {
				t.Fatal(err)
			}
			if tc != "empty file" {
				for index, data := range []string{"abcd", "ef"} {
					if err := SendTransferChunk(ctx, address, plan.TransferID, index, []byte(data)); err != nil {
						t.Fatal(err)
					}
				}
			}
			transfer := receiver.transfers[plan.TransferID]
			if tc == "corrupt staged chunk" {
				if err := os.WriteFile(filepath.Join(transfer.root, "sample.txt", "chunk_0.chunk"), []byte("XXXX"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tc == "expired" {
				transfer.expires = time.Now().Add(-time.Second)
			}
			err = CompleteChunkTransfer(ctx, address, plan.TransferID)
			if tc == "empty file" {
				if err != nil || calls != 1 {
					t.Fatalf("empty completion: calls=%d err=%v", calls, err)
				}
			} else {
				if err == nil || transfer.completed {
					t.Fatal("failed transfer marked complete")
				}
				if tc != "reconstruction error" && calls != 0 {
					t.Fatal("invalid transfer reconstructed")
				}
			}
		})
	}
}
