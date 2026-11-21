// Author: Rabindra Neupane
package network

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rabindra Neupane
// sqliteChunkLocationStore keeps the coordinator's device-to-chunk inventory
// in the same persistent database as device registration.
type sqliteChunkLocationStore struct {
	database *sql.DB
}

// Rabindra Neupane
// newSQLiteChunkLocationStore creates the persistent inventory table and its
// hash index before chunk-location requests are accepted.
func newSQLiteChunkLocationStore(database *sql.DB) (*sqliteChunkLocationStore, error) {
	// Reject startup without the shared FileSync database.
	if database == nil {
		return nil, fmt.Errorf("chunk-location database is unavailable")
	}

	// Create one unique device-and-hash row for every reported chunk location.
	if _, tableCreationError := database.Exec(`CREATE TABLE IF NOT EXISTS chunk_locations (
		device_id TEXT NOT NULL,
		chunk_hash TEXT NOT NULL,
		PRIMARY KEY (device_id, chunk_hash)
	)`); tableCreationError != nil {
		return nil, fmt.Errorf("create chunk-location table: %w", tableCreationError)
	}

	// Index hash lookups because reconstruction searches by chunk hash.
	if _, indexCreationError := database.Exec(`CREATE INDEX IF NOT EXISTS idx_chunk_locations_hash
		ON chunk_locations(chunk_hash)`); indexCreationError != nil {
		return nil, fmt.Errorf("create chunk-location index: %w", indexCreationError)
	}

	// Return the ready persistent store.
	return &sqliteChunkLocationStore{database: database}, nil
}

// Rabindra Neupane
// ReplaceDeviceChunks atomically replaces one device's complete inventory.
func (store *sqliteChunkLocationStore) ReplaceDeviceChunks(requestContext context.Context, deviceID string, chunkHashes []string) error {
	// Start one transaction so readers never observe a partially replaced list.
	databaseTransaction, transactionStartError := store.database.BeginTx(requestContext, nil)
	if transactionStartError != nil {
		return transactionStartError
	}
	defer databaseTransaction.Rollback()

	// Remove locations the device no longer reports.
	if _, inventoryDeletionError := databaseTransaction.ExecContext(requestContext, "DELETE FROM chunk_locations WHERE device_id=?", deviceID); inventoryDeletionError != nil {
		return inventoryDeletionError
	}

	// Insert every verified unique hash from the replacement inventory.
	for _, chunkHash := range chunkHashes {
		if _, inventoryInsertError := databaseTransaction.ExecContext(requestContext,
			"INSERT INTO chunk_locations(device_id,chunk_hash) VALUES(?,?)", deviceID, chunkHash); inventoryInsertError != nil {
			return inventoryInsertError
		}
	}

	// Publish the replacement only after every row is stored.
	return databaseTransaction.Commit()
}

// Rabindra Neupane
// DeviceIDsForChunk returns each device that most recently reported the hash.
func (store *sqliteChunkLocationStore) DeviceIDsForChunk(requestContext context.Context, chunkHash string) ([]string, error) {
	// Read matching device IDs in stable order for predictable peer selection.
	locationRows, locationQueryError := store.database.QueryContext(requestContext,
		"SELECT device_id FROM chunk_locations WHERE chunk_hash=? ORDER BY device_id", chunkHash)
	if locationQueryError != nil {
		return nil, locationQueryError
	}
	defer locationRows.Close()

	// Collect every matching registered-device identity.
	deviceIDs := make([]string, 0)
	for locationRows.Next() {
		var deviceID string
		if deviceIDReadingError := locationRows.Scan(&deviceID); deviceIDReadingError != nil {
			return nil, deviceIDReadingError
		}
		deviceIDs = append(deviceIDs, deviceID)
	}
	if locationRowsError := locationRows.Err(); locationRowsError != nil {
		return nil, locationRowsError
	}
	return deviceIDs, nil
}

// Reuse connections when reconstruction retrieves many chunks from one peer.
var chunkRetrievalHTTPClient = &http.Client{
	Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 8},
	Timeout:   10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Rabindra Neupane
// SetChunkLocationStore connects the chunk-location database implementation.
// The device registry uses it only to match chunks with registered devices.
func (registry *DeviceRegistry) SetChunkLocationStore(store ChunkLocationStore) {
	// Lock the registry while replacing its optional storage connection.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.chunkLocations = store
}

// Rabindra Neupane
// validLocationHash checks that a chunk hash is a lowercase SHA-256 value.
func validLocationHash(hash string) bool {
	// Decode the value and require exactly 32 lowercase SHA-256 bytes.
	decodedHash, chunkHashDecodingError := hex.DecodeString(hash)
	return chunkHashDecodingError == nil && len(decodedHash) == 32 && hash == strings.ToLower(hash)
}

// Rabindra Neupane
// ReportChunks replaces this device's chunk inventory on the coordinator.
// An empty slice clears locations that the device no longer stores.
func (registry *DeviceRegistry) ReportChunks(requestContext context.Context, chunkHashes []string) error {
	// Read the current identity and coordinator selection.
	registrationStatus := registry.Status()
	if !registrationStatus.Registered || registrationStatus.DeviceID == "" {
		return fmt.Errorf("register this device before reporting chunks")
	}

	// Validate and deduplicate hashes before sending or saving the inventory.
	uniqueChunkHashes := make([]string, 0, len(chunkHashes))
	seenChunkHashes := make(map[string]bool, len(chunkHashes))
	for _, chunkHash := range chunkHashes {
		if !validLocationHash(chunkHash) {
			return fmt.Errorf("chunk hashes must be lowercase SHA-256 hexadecimal strings")
		}
		if !seenChunkHashes[chunkHash] {
			uniqueChunkHashes = append(uniqueChunkHashes, chunkHash)
			seenChunkHashes[chunkHash] = true
		}
	}

	// Update coordinator storage directly when this device owns the registry.
	if registrationStatus.Coordinator {
		registry.mutex.Lock()
		chunkStore := registry.chunkLocations
		registry.mutex.Unlock()
		if chunkStore == nil {
			return fmt.Errorf("coordinator chunk-location storage is not connected")
		}
		return chunkStore.ReplaceDeviceChunks(requestContext, registrationStatus.DeviceID, uniqueChunkHashes)
	}

	// Send the complete inventory to the selected coordinator.
	report := ChunkReport{DeviceID: registrationStatus.DeviceID, ChunkHashes: uniqueChunkHashes}
	var result struct {
		Accepted   bool `json:"accepted"`
		ChunkCount int  `json:"chunkCount"`
	}
	if chunkReportError := registry.registryRequest(requestContext, registrationStatus.CoordinatorAddress, "/chunks/report", report, &result); chunkReportError != nil {
		return chunkReportError
	}
	if !result.Accepted || result.ChunkCount != len(uniqueChunkHashes) {
		return fmt.Errorf("coordinator did not accept the complete chunk inventory")
	}
	return nil
}

// Rabindra Neupane
// LocateChunk returns online registered devices that report a verified chunk.
func (registry *DeviceRegistry) LocateChunk(requestContext context.Context, chunkHash string) ([]Device, error) {
	// Validate the requested hash before querying local or remote storage.
	if !validLocationHash(chunkHash) {
		return nil, fmt.Errorf("provide a lowercase SHA-256 hash")
	}
	registrationStatus := registry.Status()
	if !registrationStatus.Registered || registrationStatus.DeviceID == "" {
		return nil, fmt.Errorf("register this device before locating chunks")
	}

	// Query coordinator storage directly when this device owns the registry.
	if registrationStatus.Coordinator {
		registry.mutex.Lock()
		chunkStore := registry.chunkLocations
		registry.mutex.Unlock()
		if chunkStore == nil {
			return nil, fmt.Errorf("coordinator chunk-location storage is not connected")
		}
		deviceIDs, chunkLocationQueryError := chunkStore.DeviceIDsForChunk(requestContext, chunkHash)
		if chunkLocationQueryError != nil {
			return nil, chunkLocationQueryError
		}
		registeredDevices, deviceRegistryReadingError := registry.readRegistry(requestContext)
		if deviceRegistryReadingError != nil {
			return nil, deviceRegistryReadingError
		}
		devicesHoldingChunk := make(map[string]bool, len(deviceIDs))
		for _, deviceID := range deviceIDs {
			devicesHoldingChunk[deviceID] = true
		}
		locations := make([]Device, 0)
		for _, registeredDevice := range registeredDevices {
			if registeredDevice.DeviceOnline && devicesHoldingChunk[registeredDevice.DeviceID] {
				locations = append(locations, registeredDevice)
			}
		}
		return locations, nil
	}

	// Ask the selected coordinator for currently available chunk sources.
	var locations ChunkLocations
	requestPath := "/chunks/locations?hash=" + chunkHash
	if chunkLocationRequestError := registry.registryRequest(requestContext, registrationStatus.CoordinatorAddress, requestPath, nil, &locations); chunkLocationRequestError != nil {
		return nil, chunkLocationRequestError
	}
	if locations.ChunkHash != chunkHash {
		return nil, fmt.Errorf("coordinator returned locations for a different chunk")
	}
	if locations.Devices == nil {
		locations.Devices = make([]Device, 0)
	}
	return locations.Devices, nil
}

// Rabindra Neupane
// RetrieveChunk locates online peers and returns the first valid chunk copy.
// Each downloaded result is hashed again before FileSync accepts its bytes.
func (registry *DeviceRegistry) RetrieveChunk(requestContext context.Context, chunkHash string) ([]byte, Device, error) {
	// Ask the coordinator for currently available sources.
	availableDevices, chunkLocationError := registry.LocateChunk(requestContext, chunkHash)
	if chunkLocationError != nil {
		return nil, Device{}, chunkLocationError
	}
	if len(availableDevices) == 0 {
		return nil, Device{}, fmt.Errorf("no online device contains chunk %s", chunkHash)
	}

	// Try each source until one returns the exact requested chunk.
	var lastRetrievalError error
	for _, availableDevice := range availableDevices {
		chunkData, chunkRetrievalError := retrieveChunkFromDevice(requestContext, availableDevice, chunkHash)
		if chunkRetrievalError == nil {
			return chunkData, availableDevice, nil
		}
		lastRetrievalError = chunkRetrievalError
	}

	// Return the last peer failure after every available source was attempted.
	return nil, Device{}, fmt.Errorf("cannot retrieve chunk %s from any online device: %w", chunkHash, lastRetrievalError)
}

// Rabindra Neupane
// retrieveChunkFromDevice downloads one bounded chunk and verifies its hash.
func retrieveChunkFromDevice(requestContext context.Context, device Device, chunkHash string) ([]byte, error) {
	// Reject incomplete or offline coordinator records before making a request.
	if !device.DeviceOnline || device.DeviceIP == "" || device.DevicePort < 1 || device.DevicePort > 65535 {
		return nil, fmt.Errorf("device %q is not an available chunk source", device.DeviceName)
	}

	// Create a bounded request to the selected peer's chunk endpoint.
	requestAddress := "http://" + net.JoinHostPort(device.DeviceIP, fmt.Sprintf("%d", device.DevicePort)) + "/chunks/data?hash=" + chunkHash
	httpRequest, chunkRequestCreationError := http.NewRequestWithContext(requestContext, http.MethodGet, requestAddress, nil)
	if chunkRequestCreationError != nil {
		return nil, chunkRequestCreationError
	}
	// Download the candidate chunk from this peer.
	httpResponse, chunkRequestError := chunkRetrievalHTTPClient.Do(httpRequest)
	if chunkRequestError != nil {
		return nil, fmt.Errorf("request chunk from %s: %w", device.DeviceName, chunkRequestError)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		responseDetails, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return nil, fmt.Errorf("device %s returned %s: %s", device.DeviceName, httpResponse.Status, strings.TrimSpace(string(responseDetails)))
	}

	// Read one byte beyond the chunk limit so oversized responses are rejected.
	chunkData, chunkReadingError := io.ReadAll(io.LimitReader(httpResponse.Body, maxTransferChunkSize+1))
	if chunkReadingError != nil {
		return nil, chunkReadingError
	}
	if len(chunkData) > maxTransferChunkSize {
		return nil, fmt.Errorf("device %s returned a chunk larger than the transfer limit", device.DeviceName)
	}

	// Verify the peer returned the exact content requested by hash.
	downloadedHash := sha256.Sum256(chunkData)
	if hex.EncodeToString(downloadedHash[:]) != chunkHash {
		return nil, fmt.Errorf("device %s returned a chunk with the wrong SHA-256 hash", device.DeviceName)
	}
	return chunkData, nil
}

// Rabindra Neupane
// chunkDataHandler serves verified accepted chunks by content hash.
// Transfer staging is excluded because those chunks are not accepted yet.
func chunkDataHandler(projectRoot string) http.HandlerFunc {
	// Keep the accepted chunk root fixed when the server route is created.
	acceptedChunkRoot := filepath.Join(projectRoot, "File Chunks")
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		// Allow only read requests for peer chunk retrieval.
		if request.Method != http.MethodGet {
			responseWriter.Header().Set("Allow", http.MethodGet)
			http.Error(responseWriter, "Only GET requests are allowed", http.StatusMethodNotAllowed)
			return
		}

		// Validate the requested SHA-256 value before searching local storage.
		requestedHash := request.URL.Query().Get("hash")
		if !validLocationHash(requestedHash) {
			http.Error(responseWriter, "Provide a lowercase SHA-256 hash", http.StatusBadRequest)
			return
		}

		// Read accepted file caches and ignore private transfer staging.
		fileFolders, chunkRootReadingError := os.ReadDir(acceptedChunkRoot)
		if chunkRootReadingError != nil {
			if os.IsNotExist(chunkRootReadingError) {
				http.Error(responseWriter, "Chunk not found", http.StatusNotFound)
				return
			}
			http.Error(responseWriter, "Cannot read chunk storage", http.StatusInternalServerError)
			return
		}

		// Search accepted file folders for content matching the requested hash.
		for _, fileFolder := range fileFolders {
			if !fileFolder.IsDir() || fileFolder.Name() == ".transfers" {
				continue
			}
			chunkFiles, chunkFolderReadingError := os.ReadDir(filepath.Join(acceptedChunkRoot, fileFolder.Name()))
			if chunkFolderReadingError != nil {
				continue
			}
			for _, chunkFile := range chunkFiles {
				if chunkFile.IsDir() || !strings.HasSuffix(chunkFile.Name(), ".chunk") {
					continue
				}
				chunkPath := filepath.Join(acceptedChunkRoot, fileFolder.Name(), chunkFile.Name())
				chunkData, chunkReadingError := readBoundedChunkFile(chunkPath)
				if chunkReadingError != nil {
					continue
				}
				storedHash := sha256.Sum256(chunkData)
				if hex.EncodeToString(storedHash[:]) == requestedHash {
					responseWriter.Header().Set("Content-Type", "application/octet-stream")
					responseWriter.Header().Set("X-Chunk-SHA256", requestedHash)
					responseWriter.WriteHeader(http.StatusOK)
					_, _ = responseWriter.Write(chunkData)
					return
				}
			}
		}

		// Report that this peer no longer has the advertised chunk.
		http.Error(responseWriter, "Chunk not found", http.StatusNotFound)
	}
}

// Rabindra Neupane
// readBoundedChunkFile reads one local chunk without exceeding the transfer limit.
func readBoundedChunkFile(chunkPath string) ([]byte, error) {
	// Open the candidate chunk and close it after the bounded read.
	chunkFile, chunkOpeningError := os.Open(chunkPath)
	if chunkOpeningError != nil {
		return nil, chunkOpeningError
	}
	defer chunkFile.Close()

	// Read one extra byte so an oversized local file cannot be served.
	chunkData, chunkReadingError := io.ReadAll(io.LimitReader(chunkFile, maxTransferChunkSize+1))
	if chunkReadingError != nil {
		return nil, chunkReadingError
	}
	if len(chunkData) > maxTransferChunkSize {
		return nil, fmt.Errorf("chunk exceeds transfer limit")
	}
	return chunkData, nil
}

// Rabindra Neupane
// reportChunks records the chunks stored by a registered device.
func (registry *DeviceRegistry) reportChunks(responseWriter http.ResponseWriter, request *http.Request) {
	// Allow only POST requests when reporting chunks.
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", http.MethodPost)
		http.Error(responseWriter, "Only POST requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	// Preserve the exact body before decoding and authenticating it.
	requestBody, requestBodyReadingError := readAuthenticationBody(responseWriter, request)
	if requestBodyReadingError != nil {
		http.Error(responseWriter, "Cannot read chunk report", http.StatusBadRequest)
		return
	}
	// Read the device identity and complete chunk inventory.
	var report ChunkReport
	chunkReportReadingError := readTransferJSON(responseWriter, request, &report)
	// Require a complete registered identity and a non-nil inventory list.
	if chunkReportReadingError != nil || report.DeviceID == "" || report.ChunkHashes == nil {
		http.Error(responseWriter, "Provide deviceId and chunkHashes (use [] to clear inventory)", http.StatusBadRequest)
		return
	}
	// Verify the signed device identity before accepting its inventory.
	registeredDeviceID, chunkReportAuthenticationError := registry.authenticateRegisteredRequest(request, requestBody, report.DeviceID)
	if chunkReportAuthenticationError != nil {
		http.Error(responseWriter, chunkReportAuthenticationError.Error(), http.StatusUnauthorized)
		return
	}

	// Validate and deduplicate the reported chunk hashes.
	chunkHashes := make([]string, 0, len(report.ChunkHashes))
	seenHashes := make(map[string]bool)
	// Check every reported hash before saving the complete inventory.
	for _, chunkHash := range report.ChunkHashes {
		// Reject any value that is not a lowercase SHA-256 hash.
		if !validLocationHash(chunkHash) {
			http.Error(responseWriter, "Chunk hashes must be lowercase SHA-256 hexadecimal strings", http.StatusBadRequest)
			return
		}
		// Add each valid hash only once.
		if !seenHashes[chunkHash] {
			chunkHashes = append(chunkHashes, chunkHash)
			seenHashes[chunkHash] = true
		}
	}
	// Lock the registry while checking storage and coordinator state.
	registry.mutex.Lock()
	chunkStore := registry.chunkLocations
	isCoordinator := registry.settings.Coordinator
	registry.mutex.Unlock()
	// Confirm the coordinator and chunk-location storage are available.
	if !isCoordinator || chunkStore == nil {
		http.Error(responseWriter, "Coordinator chunk-location storage is not connected", http.StatusServiceUnavailable)
		return
	}

	// Replace the device's complete chunk inventory in one operation.
	chunkInventorySavingError := chunkStore.ReplaceDeviceChunks(request.Context(), registeredDeviceID, chunkHashes)
	if chunkInventorySavingError != nil {
		http.Error(responseWriter, "Cannot save chunk inventory", http.StatusInternalServerError)
		return
	}

	// Respond with the number of unique chunks that were accepted.
	writeTransferJSON(responseWriter, struct {
		Accepted   bool `json:"accepted"`
		ChunkCount int  `json:"chunkCount"`
	}{true, len(chunkHashes)})
}

// Rabindra Neupane
// locateChunk returns the online registered devices that hold a chunk.
func (registry *DeviceRegistry) locateChunk(responseWriter http.ResponseWriter, request *http.Request) {
	// Allow only GET requests for locating chunks.
	if request.Method != http.MethodGet {
		responseWriter.Header().Set("Allow", http.MethodGet)
		http.Error(responseWriter, "Only GET requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	// Require a registered signed request before exposing chunk locations.
	if _, chunkLookupAuthenticationError := registry.authenticateRegisteredRequest(request, nil, ""); chunkLookupAuthenticationError != nil {
		http.Error(responseWriter, chunkLookupAuthenticationError.Error(), http.StatusUnauthorized)
		return
	}

	// Read and validate the requested chunk hash from the query.
	chunkHash := request.URL.Query().Get("hash")
	// Reject an invalid or missing SHA-256 value.
	if !validLocationHash(chunkHash) {
		http.Error(responseWriter, "Provide a lowercase SHA-256 hash", http.StatusBadRequest)
		return
	}

	// Lock briefly while copying the coordinator and storage state.
	registry.mutex.Lock()
	chunkStore := registry.chunkLocations
	isCoordinator := registry.settings.Coordinator
	registry.mutex.Unlock()
	// Require this device to be the connected chunk-location coordinator.
	if !isCoordinator || chunkStore == nil {
		http.Error(responseWriter, "Coordinator chunk-location storage is not connected", http.StatusServiceUnavailable)
		return
	}

	// Search persistent storage for device IDs that report this chunk.
	deviceIDs, chunkLocationQueryError := chunkStore.DeviceIDsForChunk(request.Context(), chunkHash)
	if chunkLocationQueryError != nil {
		http.Error(responseWriter, "Cannot look up chunk locations", http.StatusInternalServerError)
		return
	}

	// Read registered devices so offline locations can be filtered out.
	registeredDevices, deviceRegistryReadingError := registry.readRegistry(request.Context())
	if deviceRegistryReadingError != nil {
		http.Error(responseWriter, "Cannot read registered devices", http.StatusServiceUnavailable)
		return
	}

	// Create a lookup map for the device IDs returned by storage.
	devicesHoldingChunk := make(map[string]bool, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		// Mark this device as a reported location for the requested chunk.
		devicesHoldingChunk[deviceID] = true
	}

	// Build the response from online devices that hold the chunk.
	locations := ChunkLocations{ChunkHash: chunkHash, Devices: make([]Device, 0)}
	// Keep only online registered devices found in the location map.
	for _, registeredDevice := range registeredDevices {
		// Add an available source when both checks pass.
		if registeredDevice.DeviceOnline && devicesHoldingChunk[registeredDevice.DeviceID] {
			locations.Devices = append(locations.Devices, registeredDevice)
		}
	}

	// Respond with the available devices for this chunk.
	writeTransferJSON(responseWriter, locations)
}
