package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCompareFileMetadataRoundTrip(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/file-metadata" || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		compareFileMetadata(w, r, root)
	}))
	defer server.Close()
	result, err := CompareFileMetadata(context.Background(), strings.TrimPrefix(server.URL, "http://"), testFileMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.NeededChunks, []int{0, 1}) {
		t.Fatalf("unexpected chunks: %v", result.NeededChunks)
	}
}

func TestCompareFileMetadataResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"unchanged", `{"neededChunks":[]}`, 200, true},
		{"one chunk", `{"neededChunks":[1]}`, 200, true},
		{"HTTP failure", "receiver unavailable", 503, false},
		{"redirect", "", 302, false},
		{"malformed", "{", 200, false},
		{"missing list", `{}`, 200, false},
		{"null response", `null`, 200, false},
		{"null list", `{"neededChunks":null}`, 200, false},
		{"negative", `{"neededChunks":[-1]}`, 200, false},
		{"out of range", `{"neededChunks":[2]}`, 200, false},
		{"duplicate", `{"neededChunks":[0,0]}`, 200, false},
		{"unknown field", `{"neededChunks":[],"other":1}`, 200, false},
		{"trailing JSON", `{"neededChunks":[]} {}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var received FileMetadata
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(received, testFileMetadata()) {
					t.Error("metadata changed in transit")
				}
				if tc.status == 302 {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result, err := CompareFileMetadata(context.Background(), strings.TrimPrefix(server.URL, "http://"), testFileMetadata())
			if tc.valid && (err != nil || result == nil) {
				t.Fatalf("expected success: %v", err)
			}
			if !tc.valid && (err == nil || result != nil) {
				t.Fatalf("expected failure: %v", result)
			}
		})
	}
}

func TestCompareFileMetadataRejectsInvalidInput(t *testing.T) {
	metadata := testFileMetadata()
	metadata.ChunkSize = 0
	if _, err := CompareFileMetadata(context.Background(), "unused:8080", metadata); err == nil {
		t.Fatal("accepted invalid metadata")
	}
}

func TestCompareFileMetadataTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	client := &http.Client{Timeout: 50 * time.Millisecond}
	defer client.CloseIdleConnections()
	_, err := requestFileMetadata(context.Background(), client, strings.TrimPrefix(server.URL, "http://"), testFileMetadata())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestCompareFileMetadataCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CompareFileMetadata(ctx, "127.0.0.1:1", testFileMetadata())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestCompareFileMetadataConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	address := strings.TrimPrefix(server.URL, "http://")
	server.Close()
	if _, err := CompareFileMetadata(context.Background(), address, testFileMetadata()); err == nil {
		t.Fatal("expected connection failure")
	}
}
