package network

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type testNode struct {
	root      string
	configDir string
	db        *sql.DB
	registry  *DeviceRegistry
	listener  net.Listener
	addr      string
	cancel    context.CancelFunc
	chunkIdx  *ChunkFileIndex
	files     map[string]FileMetadata
}

func startTestNode(t *testing.T, metadataProvider func(string) (FileMetadata, error)) *testNode {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	root := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "cfg")
	chunkRoot := filepath.Join(root, "File Chunks")
	if err := os.MkdirAll(chunkRoot, 0755); err != nil {
		cancel()
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(root, "node.db"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS device_registry (
		registration_key TEXT NOT NULL UNIQUE,
		device_id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		ip TEXT NOT NULL,
		port INTEGER NOT NULL,
		last_seen INTEGER NOT NULL
	)`); err != nil {
		cancel()
		db.Close()
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		db.Close()
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	registry, err := OpenDeviceRegistryWithConfigDir(ctx, db, configDir, port)
	if err != nil {
		cancel()
		listener.Close()
		db.Close()
		t.Fatal(err)
	}
	chunkIdx := NewChunkFileIndex(chunkRoot)
	registry.SetChunkFileIndex(chunkIdx)

	node := &testNode{
		root:      root,
		configDir: configDir,
		db:        db,
		registry:  registry,
		listener:  listener,
		addr:      listener.Addr().String(),
		cancel:    cancel,
		chunkIdx:  chunkIdx,
		files:     make(map[string]FileMetadata),
	}
	provider := metadataProvider
	if provider == nil {
		provider = func(name string) (FileMetadata, error) {
			if m, ok := node.files[name]; ok {
				return m, nil
			}
			return FileMetadata{}, fmt.Errorf("file not found")
		}
	}

	go StartServerAtRoot(ctx, listener, root, func(meta FileMetadata, stagingRoot string) error {
		return nil
	}, registry, nil, provider)

	t.Cleanup(func() {
		cancel()
		registry.Close()
		listener.Close()
		db.Close()
	})
	return node
}

func waitForRegistered(t *testing.T, registry *DeviceRegistry) RegistrationStatus {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		status := registry.Status()
		if status.Registered && status.DeviceID != "" {
			return status
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for registration (state=%s err=%s)", registry.Status().State, registry.Status().LastError)
	return RegistrationStatus{}
}

func makeSampleMetadata(deviceID, fileName string, content []byte) (FileMetadata, []byte) {
	chunkSum := sha256.Sum256(content)
	chunkHash := hex.EncodeToString(chunkSum[:])
	return FileMetadata{
		BaseHash:    "",
		DeviceID:    deviceID,
		FileID:      fileName,
		FileName:    fileName,
		Version:     1,
		FileSize:    int64(len(content)),
		ChunkSize:   4096,
		ChunkHashes: []string{chunkHash},
		FileHash:    chunkHash,
	}, content
}

func TestSecurityRoutesRequireVerifiedMTLS(t *testing.T) {
	coord := startTestNode(t, nil)
	if err := coord.registry.Configure(true, "", ""); err != nil {
		t.Fatal(err)
	}
	coordStatus := waitForRegistered(t, coord.registry)

	peer := startTestNode(t, nil)
	pairing, err := coord.registry.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.registry.Configure(false, coord.addr, pairing.Code); err != nil {
		t.Fatal(err)
	}
	peerStatus := waitForRegistered(t, peer.registry)

	// Prepare an indexed chunk and metadata on coord so /files/metadata and /chunks/data exist.
	sample := []byte("hello-secure-chunk")
	meta, _ := makeSampleMetadata(coordStatus.DeviceID, "secret.txt", sample)
	coord.files["secret.txt"] = meta
	chunkDir := filepath.Join(coord.root, "File Chunks", "secret.txt")
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		t.Fatal(err)
	}
	chunkPath := filepath.Join(chunkDir, "chunk_0.chunk")
	if err := os.WriteFile(chunkPath, sample, 0644); err != nil {
		t.Fatal(err)
	}
	coord.chunkIdx.Add(meta.ChunkHashes[0], chunkPath)

	// 1. Plain HTTPS client without client cert must be rejected by TLS RequireAndVerifyClientCert.
	noCertTLS := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true,
			},
		},
		Timeout: 3 * time.Second,
	}
	if _, err := noCertTLS.Get("https://" + coord.addr + "/files/metadata?name=secret.txt"); err == nil {
		t.Fatal("expected unauthenticated TLS client without certificate to be rejected")
	}

	// 2. Client attempting to negotiate bootstrap ALPN must still fail HTTP auth on ALL peer & coordinator protected routes.
	bootstrapBypass := bootstrapTLSHTTPClient("", 3*time.Second, nil)
	defer bootstrapBypass.CloseIdleConnections()
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/transfer/start", `{}`},
		{http.MethodPost, "/transfer/chunk?transferId=abc&chunkIndex=0", `x`},
		{http.MethodPost, "/transfer/complete", `{"transferId":"abc"}`},
		{http.MethodPost, "/file", ``},
		{http.MethodGet, "/files/metadata?name=secret.txt", ``},
		{http.MethodGet, "/chunks/data?hash=" + meta.ChunkHashes[0], ``},
		{http.MethodGet, "/devices", ``},
		{http.MethodPost, "/devices/heartbeat", `{}`},
		{http.MethodPost, "/chunks/report", `{}`},
		{http.MethodGet, "/chunks/locations?hash=" + meta.ChunkHashes[0], ``},
	}
	for _, tc := range routes {
		req, _ := http.NewRequest(tc.method, "https://"+coord.addr+tc.path, strings.NewReader(tc.body))
		resp, reqErr := bootstrapBypass.Do(req)
		if reqErr == nil {
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("route %s %s returned status %d via bootstrap SNI, want 401", tc.method, tc.path, resp.StatusCode)
			}
		}
	}

	// 3. /ping over bootstrap TLS works and advertises coordinator CA fingerprint.
	pingResp, err := PingDevice(coord.addr)
	if err != nil {
		t.Fatalf("PingDevice failed: %v", err)
	}
	if !pingResp.Coordinator || pingResp.CAFingerprint == "" {
		t.Fatalf("expected coordinator ping response with CA fingerprint, got %+v", pingResp)
	}

	// 4. Rogue self-signed client certificate (not signed by Coordinator CA) must fail TLS verification.
	rogueKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial := big.NewInt(999)
	rogueTmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: peerStatus.DeviceID},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	rogueDER, _ := x509.CreateCertificate(rand.Reader, &rogueTmpl, &rogueTmpl, &rogueKey.PublicKey, rogueKey)
	rogueClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{{Certificate: [][]byte{rogueDER}, PrivateKey: rogueKey}},
			},
		},
		Timeout: 3 * time.Second,
	}
	if _, rogueErr := rogueClient.Get("https://" + coord.addr + "/chunks/data?hash=" + meta.ChunkHashes[0]); rogueErr == nil {
		t.Fatal("expected rogue self-signed client cert to be rejected")
	}

	// 5. Authorized peer cannot spoof another DeviceID in FileMetadata.DeviceID on /transfer/start or /file.
	spoofMeta, spoofBytes := makeSampleMetadata(coordStatus.DeviceID, "spoof.txt", []byte("spoofed"))
	if _, err := StartChunkTransfer(context.Background(), coord.addr, spoofMeta, peer.registry.TransferHTTPClient()); err == nil {
		t.Fatal("expected spoofed DeviceID on /transfer/start to be rejected")
	}
	if err := SendFile(context.Background(), coord.addr, spoofMeta, spoofBytes, peer.registry.TransferHTTPClient()); err == nil {
		t.Fatal("expected spoofed DeviceID on /file to be rejected")
	}
}

func TestRevokedDeviceRejectedAcrossCoordinatorAndPeersAndSecretsProtected(t *testing.T) {
	coord := startTestNode(t, nil)
	if err := coord.registry.Configure(true, "", ""); err != nil {
		t.Fatal(err)
	}
	waitForRegistered(t, coord.registry)

	peerA := startTestNode(t, nil)
	peerB := startTestNode(t, nil)
	pairing, err := coord.registry.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	if err := peerA.registry.Configure(false, coord.addr, pairing.Code); err != nil {
		t.Fatal(err)
	}
	if err := peerB.registry.Configure(false, coord.addr, pairing.Code); err != nil {
		t.Fatal(err)
	}
	statusA := waitForRegistered(t, peerA.registry)
	statusB := waitForRegistered(t, peerB.registry)

	// Verify Priority 3 secret protection on disk.
	settingsBytes, err := os.ReadFile(filepath.Join(peerB.configDir, deviceSettingsFileName))
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, err := os.ReadFile(filepath.Join(peerB.configDir, peerKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if !bytes.Contains(settingsBytes, []byte("DPAPIv1:")) {
			t.Fatalf("expected registrationKey in .filesync-device.json to be DPAPI-encrypted on Windows")
		}
		if !bytes.HasPrefix(keyBytes, []byte("DPAPIv1:")) {
			t.Fatalf("expected peer-key.pem to be DPAPI-encrypted on Windows")
		}
	}
	defaultDir, err := defaultSecurityConfigDir()
	if err != nil || !strings.HasSuffix(defaultDir, filepath.Join("FileSync")) {
		t.Fatalf("unexpected defaultSecurityConfigDir %q: %v", defaultDir, err)
	}

	// Host chunk + metadata on peerA so peerB can verify mTLS peer transfer first.
	sample := []byte("authorized-peer-payload")
	metaA, _ := makeSampleMetadata(statusA.DeviceID, "shared.txt", sample)
	peerA.files["shared.txt"] = metaA
	chunkDirA := filepath.Join(peerA.root, "File Chunks", "shared.txt")
	if err := os.MkdirAll(chunkDirA, 0755); err != nil {
		t.Fatal(err)
	}
	chunkPathA := filepath.Join(chunkDirA, "chunk_0.chunk")
	if err := os.WriteFile(chunkPathA, sample, 0644); err != nil {
		t.Fatal(err)
	}
	peerA.chunkIdx.Add(metaA.ChunkHashes[0], chunkPathA)

	// Before revocation: peerB succeeds on /files/metadata, /chunks/data, /transfer/start, /file against peerA.
	if _, err := FetchFileMetadata(context.Background(), peerA.addr, "shared.txt", peerB.registry.TransferHTTPClient()); err != nil {
		t.Fatalf("authorized peerB metadata fetch failed: %v", err)
	}
	if got, err := FetchChunkFromAddress(context.Background(), peerA.addr, metaA.ChunkHashes[0], peerB.registry.ChunkRetrievalHTTPClient()); err != nil || !bytes.Equal(got, sample) {
		t.Fatalf("authorized peerB chunk fetch failed: %v", err)
	}
	metaB, dataB := makeSampleMetadata(statusB.DeviceID, "upload.txt", []byte("from-peer-B"))
	if err := SendFile(context.Background(), peerA.addr, metaB, dataB, peerB.registry.TransferHTTPClient()); err != nil {
		t.Fatalf("authorized peerB whole-file send failed: %v", err)
	}

	// Revoke peerB on Coordinator.
	if err := coord.registry.RevokeDevice(statusB.DeviceID); err != nil {
		t.Fatalf("RevokeDevice failed: %v", err)
	}

	// Immediately after RevokeDevice:
	// 1. Coordinator must reject peerB (/devices, /chunks/locations, /chunks/report).
	if _, err := peerB.registry.Devices(); err == nil {
		t.Fatal("expected revoked peerB coordinator Devices() query to fail")
	}
	// 2. PeerA must reject peerB immediately across all peer-to-peer endpoints (/files/metadata, /chunks/data, /transfer/start, /file).
	if _, err := FetchFileMetadata(context.Background(), peerA.addr, "shared.txt", peerB.registry.TransferHTTPClient()); err == nil {
		t.Fatal("expected revoked peerB metadata fetch on peerA to fail")
	}
	if _, err := FetchChunkFromAddress(context.Background(), peerA.addr, metaA.ChunkHashes[0], peerB.registry.ChunkRetrievalHTTPClient()); err == nil {
		t.Fatal("expected revoked peerB chunk fetch on peerA to fail")
	}
	if _, err := StartChunkTransfer(context.Background(), peerA.addr, metaB, peerB.registry.TransferHTTPClient()); err == nil {
		t.Fatal("expected revoked peerB /transfer/start on peerA to fail")
	}
	if err := SendFile(context.Background(), peerA.addr, metaB, dataB, peerB.registry.TransferHTTPClient()); err == nil {
		t.Fatal("expected revoked peerB /file on peerA to fail")
	}

	// PeerA remains authorized cleanly.
	if _, err := peerA.registry.Devices(); err != nil {
		t.Fatalf("expected non-revoked peerA Devices() to still succeed: %v", err)
	}
}

func TestPriority4ChunkIndexBoundsQuotasAndCleanup(t *testing.T) {
	coord := startTestNode(t, nil)
	if err := coord.registry.Configure(true, "", ""); err != nil {
		t.Fatal(err)
	}
	waitForRegistered(t, coord.registry)

	peer := startTestNode(t, nil)
	pairing, err := coord.registry.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.registry.Configure(false, coord.addr, pairing.Code); err != nil {
		t.Fatal(err)
	}
	peerStatus := waitForRegistered(t, peer.registry)

	// 1. Indexed chunkDataHandler does NOT fall back to full directory walk when unindexed.
	unindexedBytes := []byte("unindexed-disk-chunk")
	sum := sha256.Sum256(unindexedBytes)
	unindexedHash := hex.EncodeToString(sum[:])
	unindexedDir := filepath.Join(coord.root, "File Chunks", "manual.bin")
	if err := os.MkdirAll(unindexedDir, 0755); err != nil {
		t.Fatal(err)
	}
	unindexedPath := filepath.Join(unindexedDir, "chunk_0.chunk")
	if err := os.WriteFile(unindexedPath, unindexedBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := FetchChunkFromAddress(context.Background(), coord.addr, unindexedHash, peer.registry.ChunkRetrievalHTTPClient()); err == nil {
		t.Fatal("expected unindexed chunk on disk to return 404 without directory walk")
	}
	coord.chunkIdx.Add(unindexedHash, unindexedPath)
	gotChunk, err := FetchChunkFromAddress(context.Background(), coord.addr, unindexedHash, peer.registry.ChunkRetrievalHTTPClient())
	if err != nil || !bytes.Equal(gotChunk, unindexedBytes) {
		t.Fatalf("expected indexed chunk retrieval to succeed: %v", err)
	}

	// 2. Per-peer transfer quota (maxConcurrentTransfersPerPeer = 4).
	for i := 0; i < maxConcurrentTransfersPerPeer; i++ {
		meta, _ := makeSampleMetadata(peerStatus.DeviceID, fmt.Sprintf("quota-%d.txt", i), []byte("quota-data"))
		plan, startErr := StartChunkTransfer(context.Background(), coord.addr, meta, peer.registry.TransferHTTPClient())
		if startErr != nil {
			t.Fatalf("transfer %d failed unexpectedly: %v", i, startErr)
		}
		if plan.TransferID == "" {
			t.Fatal("expected non-empty transferId")
		}
	}
	overflowMeta, _ := makeSampleMetadata(peerStatus.DeviceID, "quota-overflow.txt", []byte("quota-data"))
	_, overflowErr := StartChunkTransfer(context.Background(), coord.addr, overflowMeta, peer.registry.TransferHTTPClient())
	if overflowErr == nil {
		t.Fatal("expected 5th concurrent transfer from same peer to be rejected")
	}
	var statusErr *httpStatusError
	if !errors.As(overflowErr, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected HTTP 429 for per-peer quota overflow, got %v", overflowErr)
	}

	// 3. Bounded /file upload limit via NewWholeFileSendHandlerWithLimit (1024-byte cap).
	limitCtx, limitCancel := context.WithCancel(context.Background())
	defer limitCancel()
	limitListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer limitListener.Close()
	limitedSrv := &http.Server{
		Handler:           NewWholeFileSendHandlerWithLimit(coord.root, func(FileMetadata, string) error { return nil }, nil, coord.registry, coord.chunkIdx, 1024),
		TLSConfig:         coord.registry.ServerTLSConfig(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go limitedSrv.ServeTLS(limitListener, "", "")
	t.Cleanup(func() { limitedSrv.Shutdown(limitCtx) })

	bigData := bytes.Repeat([]byte("A"), 2048)
	bigMeta, _ := makeSampleMetadata(peerStatus.DeviceID, "too-big.bin", bigData)
	if sendErr := SendFile(context.Background(), limitListener.Addr().String(), bigMeta, bigData, peer.registry.TransferHTTPClient()); sendErr == nil {
		t.Fatal("expected oversized whole-file upload above 1024-byte cap to fail")
	}

	// 4. Transfer staging cleanup ticker removes expired staging folders.
	staleDir := filepath.Join(coord.root, "File Chunks", ".transfers", "stale-incoming-123")
	if err := os.MkdirAll(staleDir, 0755); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(staleDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	receiver := newTransferReceiver(coord.root, nil, nil, coord.registry, coord.chunkIdx)
	receiver.cleanupExpiredAndStaleStaging()
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("expected stale staging dir %s to be deleted by cleanup", staleDir)
	}
}
