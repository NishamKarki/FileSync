package network

import (
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
)

// Rabindra Neupane
// SetChunkLocationStore connects the chunk-location database implementation.
// The device registry uses it only to match chunks with registered devices.
func (registry *DeviceRegistry) SetChunkLocationStore(store ChunkLocationStore) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.chunkLocations = store
}

// Rabindra Neupane
// validLocationHash checks that a chunk hash is a lowercase SHA-256 value.
func validLocationHash(hash string) bool {
	decodedHash, chunkHashDecodingError := hex.DecodeString(hash)
	return chunkHashDecodingError == nil && len(decodedHash) == 32 && hash == strings.ToLower(hash)
}

// Rabindra Neupane
// reportChunks records the chunks stored by a registered device.
func (registry *DeviceRegistry) reportChunks(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", http.MethodPost)
		http.Error(responseWriter, "Only POST requests are allowed", http.StatusMethodNotAllowed)
		return
	}

	var report ChunkReport
	chunkReportReadingError := readTransferJSON(responseWriter, request, &report)
	if chunkReportReadingError != nil || report.DeviceID == "" || report.RegistrationKey == "" || report.ChunkHashes == nil {
		http.Error(responseWriter, "Provide deviceId, registrationKey and chunkHashes (use [] to clear inventory)", http.StatusBadRequest)
		return
	}

	chunkHashes := make([]string, 0, len(report.ChunkHashes))
	seenHashes := make(map[string]bool)
	for _, chunkHash := range report.ChunkHashes {
		if !validLocationHash(chunkHash) {
			http.Error(responseWriter, "Chunk hashes must be lowercase SHA-256 hexadecimal strings", http.StatusBadRequest)
			return
		}
		if !seenHashes[chunkHash] {
			chunkHashes = append(chunkHashes, chunkHash)
			seenHashes[chunkHash] = true
		}
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.settings.Coordinator || registry.chunkLocations == nil {
		http.Error(responseWriter, "Coordinator chunk-location storage is not connected", http.StatusServiceUnavailable)
		return
	}

	var registeredDeviceID string
	deviceRegistrationQueryError := registry.database.QueryRowContext(
		request.Context(),
		"SELECT device_id FROM device_registry WHERE registration_key=?",
		report.RegistrationKey,
	).Scan(&registeredDeviceID)

	if deviceRegistrationQueryError == sql.ErrNoRows || (deviceRegistrationQueryError == nil && registeredDeviceID != report.DeviceID) {
		http.Error(responseWriter, "Register this device before reporting chunks", http.StatusForbidden)
		return
	}
	if deviceRegistrationQueryError != nil {
		http.Error(responseWriter, "Cannot verify registered device", http.StatusInternalServerError)
		return
	}

	chunkInventorySavingError := registry.chunkLocations.ReplaceDeviceChunks(request.Context(), registeredDeviceID, chunkHashes)
	if chunkInventorySavingError != nil {
		http.Error(responseWriter, "Cannot save chunk inventory", http.StatusInternalServerError)
		return
	}

	writeTransferJSON(responseWriter, struct {
		Accepted   bool `json:"accepted"`
		ChunkCount int  `json:"chunkCount"`
	}{true, len(chunkHashes)})
}

// Rabindra Neupane
// locateChunk returns the online registered devices that hold a chunk.
func (registry *DeviceRegistry) locateChunk(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		responseWriter.Header().Set("Allow", http.MethodGet)
		http.Error(responseWriter, "Only GET requests are allowed", http.StatusMethodNotAllowed)
		return
	}

	chunkHash := request.URL.Query().Get("hash")
	if !validLocationHash(chunkHash) {
		http.Error(responseWriter, "Provide a lowercase SHA-256 hash", http.StatusBadRequest)
		return
	}

	registry.mutex.Lock()
	chunkStore := registry.chunkLocations
	isCoordinator := registry.settings.Coordinator
	registry.mutex.Unlock()
	if !isCoordinator || chunkStore == nil {
		http.Error(responseWriter, "Coordinator chunk-location storage is not connected", http.StatusServiceUnavailable)
		return
	}

	deviceIDs, chunkLocationQueryError := chunkStore.DeviceIDsForChunk(request.Context(), chunkHash)
	if chunkLocationQueryError != nil {
		http.Error(responseWriter, "Cannot look up chunk locations", http.StatusInternalServerError)
		return
	}

	registeredDevices, deviceRegistryReadingError := registry.readRegistry(request.Context())
	if deviceRegistryReadingError != nil {
		http.Error(responseWriter, "Cannot read registered devices", http.StatusServiceUnavailable)
		return
	}

	devicesHoldingChunk := make(map[string]bool, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		devicesHoldingChunk[deviceID] = true
	}

	locations := ChunkLocations{ChunkHash: chunkHash, Devices: make([]Device, 0)}
	for _, registeredDevice := range registeredDevices {
		if registeredDevice.DeviceOnline && devicesHoldingChunk[registeredDevice.DeviceID] {
			locations.Devices = append(locations.Devices, registeredDevice)
		}
	}

	writeTransferJSON(responseWriter, locations)
}
