package network

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

//Rabindra Neupane
func TestChunkHandlerReconstructionCallback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total       int
		fail        bool
		unavailable bool
		wantCalls   int
		wantStatus  int
	}{
		{"incomplete", 2, false, false, 0, http.StatusOK},
		{"complete", 1, false, false, 1, http.StatusOK},
		{"reconstruction error", 1, true, false, 1, http.StatusInternalServerError},
		{"missing callback", 1, false, true, 0, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for key, value := range map[string]string{
				"fileName": "test.txt", "chunkIndex": "0", "totalChunks": fmt.Sprint(tc.total), "chunkHash": metadataHash("abcd"),
			} {
				if err := writer.WriteField(key, value); err != nil {
					t.Fatal(err)
				}
			}
			part, err := writer.CreateFormFile("Chunk", "chunk_0.chunk")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte("abcd")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/chunk", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			calls := 0
			var callback func(string) error
			if !tc.unavailable {
				callback = func(name string) error {
					calls++
					if name != "test.txt" {
						t.Errorf("unexpected name: %s", name)
					}
					if tc.fail {
						return fmt.Errorf("reconstruction failed")
					}
					return nil
				}
			}
			response := httptest.NewRecorder()
			chunkHandler(response, request, callback)
			if response.Code != tc.wantStatus || calls != tc.wantCalls {
				t.Fatalf("status=%d calls=%d; want status=%d calls=%d", response.Code, calls, tc.wantStatus, tc.wantCalls)
			}
		})
	}
}
