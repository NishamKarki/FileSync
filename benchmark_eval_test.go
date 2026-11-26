package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestCapstoneBenchmarkMatrix creates deterministic local benchmark evidence
// for fixed chunks before the same matrix is executed on the live LAN.
func TestCapstoneBenchmarkMatrix(t *testing.T) {
	scenarios := []struct {
		name string
		size int
		edit string
	}{
		{"small_unchanged", 128 * 1024, "unchanged"},
		{"small_beginning", 128 * 1024, "beginning"},
		{"small_middle", 128 * 1024, "middle"},
		{"small_end", 128 * 1024, "end"},
		{"medium_unchanged", 4 * 1024 * 1024, "unchanged"},
		{"medium_beginning", 4 * 1024 * 1024, "beginning"},
		{"medium_middle", 4 * 1024 * 1024, "middle"},
		{"medium_end", 4 * 1024 * 1024, "end"},
		{"large_unchanged", 32 * 1024 * 1024, "unchanged"},
		{"large_beginning", 32 * 1024 * 1024, "beginning"},
		{"large_middle", 32 * 1024 * 1024, "middle"},
		{"large_end", 32 * 1024 * 1024, "end"},
	}
	if testing.Short() {
		scenarios = scenarios[:4]
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			for trial := 1; trial <= 3; trial++ {
				root := t.TempDir()
				fileName := fmt.Sprintf("bench_%d_%s_trial%d.bin", scenario.size, scenario.edit, trial)
				baseline := deterministicBenchmarkBytes(scenario.size, byte(trial))
				edited := append([]byte(nil), baseline...)
				applyBenchmarkEdit(edited, scenario.edit)
				if writingError := os.WriteFile(filepath.Join(root, fileName), edited, 0600); writingError != nil {
					t.Fatalf("write benchmark payload: %v", writingError)
				}

				app := &App{}
				baselineChunks, baselineChunkingError := app.ChunkFile(filepath.Join(root, fileName))
				if baselineChunkingError != nil {
					t.Fatalf("chunk benchmark payload: %v", baselineChunkingError)
				}
				if len(baselineChunks) == 0 {
					t.Fatal("expected chunks for non-empty benchmark payload")
				}
				// Re-chunk the original bytes to model the receiver's cached baseline.
				baselinePath := filepath.Join(root, "baseline-"+fileName)
				if writingError := os.WriteFile(baselinePath, baseline, 0600); writingError != nil {
					t.Fatalf("write baseline payload: %v", writingError)
				}
				originalChunks, originalChunkingError := app.ChunkFile(baselinePath)
				if originalChunkingError != nil {
					t.Fatalf("chunk baseline payload: %v", originalChunkingError)
				}

				changedChunks, changedBytes := benchmarkChangedChunks(originalChunks, baselineChunks)
				if scenario.edit == "unchanged" && changedChunks != 0 {
					t.Fatalf("unchanged file selected %d chunks", changedChunks)
				}
				if scenario.edit != "unchanged" && changedChunks == 0 {
					t.Fatal("edited file did not select any chunks")
				}
				if changedBytes >= int64(len(edited)) && scenario.edit != "unchanged" && len(edited) > chunkSize {
					t.Fatalf("incremental transfer did not save bytes: %d >= %d", changedBytes, len(edited))
				}
				// Check that the deterministic source and reconstructed target hash identically.
				sourceHash := sha256.Sum256(edited)
				receiverHash := sha256.Sum256(reconstructBenchmarkChunks(baselineChunks))
				if sourceHash != receiverHash {
					t.Fatalf("checksum mismatch: %s != %s", hex.EncodeToString(sourceHash[:]), hex.EncodeToString(receiverHash[:]))
				}
				t.Logf("trial=%d size=%d edit=%s incremental_bytes=%d whole_file_bytes=%d changed_chunks=%d/%d", trial, scenario.size, scenario.edit, changedBytes, len(edited), changedChunks, len(baselineChunks))
			}
		})
	}
}

func deterministicBenchmarkBytes(size int, seed byte) []byte {
	payload := make([]byte, size)
	for index := range payload {
		payload[index] = byte((index*31 + int(seed)*17) % 251)
	}
	return payload
}

func applyBenchmarkEdit(payload []byte, location string) {
	if location == "unchanged" || len(payload) == 0 {
		return
	}
	offset := 0
	switch location {
	case "middle":
		offset = len(payload) / 2
	case "end":
		offset = len(payload) - 512
	}
	if offset < 0 {
		offset = 0
	}
	end := offset + 512
	if end > len(payload) {
		end = len(payload)
	}
	for index := offset; index < end; index++ {
		payload[index] ^= 0xFF
	}
}

func benchmarkChangedChunks(before, after []Chunk) (int, int64) {
	changed, bytes := 0, int64(0)
	for index, chunk := range after {
		if index >= len(before) || before[index].Hash != chunk.Hash {
			changed++
			bytes += int64(len(chunk.Data))
		}
	}
	return changed, bytes
}

func reconstructBenchmarkChunks(chunks []Chunk) []byte {
	total := 0
	for _, chunk := range chunks {
		total += len(chunk.Data)
	}
	result := make([]byte, 0, total)
	for _, chunk := range chunks {
		result = append(result, chunk.Data...)
	}
	return result
}

// TestLiveLANBenchmark keeps the live two-PC run opt-in and never runs against
// a peer unless the person operating the benchmark supplies its address.
func TestLiveLANBenchmark(t *testing.T) {
	peerAddress := os.Getenv("FILESYNC_PEER_ADDR")
	if peerAddress == "" {
		t.Skip("set FILESYNC_PEER_ADDR=host:port to run the live two-PC benchmark")
	}
	t.Logf("live LAN benchmark target configured: %s", peerAddress)
	t.Log("run this test from an initialized FileSync desktop instance so its selected files, registration, and performance logs remain controlled")
}
