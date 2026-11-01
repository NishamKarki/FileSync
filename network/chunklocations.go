// Author: Rabindra Neupane
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
// reportChunks records the chunks stored by a registered device.
func (registry *DeviceRegistry) reportChunks(responseWriter http.ResponseWriter, request *http.Request) {
	// Allow only POST requests when reporting chunks.
	if request.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", http.MethodPost)
		http.Error(responseWriter, "Only POST requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	// Read the device identity and complete chunk inventory.
	var report ChunkReport
	chunkReportReadingError := readTransferJSON(responseWriter, request, &report)
	// Require a complete registered identity and a non-nil inventory list.
	if chunkReportReadingError != nil || report.DeviceID == "" || report.RegistrationKey == "" || report.ChunkHashes == nil {
		http.Error(responseWriter, "Provide deviceId, registrationKey and chunkHashes (use [] to clear inventory)", http.StatusBadRequest)
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
	// Lock the registry while checking storage and registration state.
	registry.mutex.Lock()
	// Release the lock when inventory processing finishes.
	defer registry.mutex.Unlock()
	// Confirm the coordinator and chunk-location storage are available.
	if !registry.settings.Coordinator || registry.chunkLocations == nil {
		http.Error(responseWriter, "Coordinator chunk-location storage is not connected", http.StatusServiceUnavailable)
		return
	}
	// Read the device ID assigned to this private registration key.
	var registeredDeviceID string
	deviceRegistrationQueryError := registry.database.QueryRowContext(
		request.Context(),
		"SELECT device_id FROM device_registry WHERE registration_key=?",
		report.RegistrationKey,
	).Scan(&registeredDeviceID)

	// Reject an unknown key or a device ID that does not match the key.
	if deviceRegistrationQueryError == sql.ErrNoRows || (deviceRegistrationQueryError == nil && registeredDeviceID != report.DeviceID) {
		http.Error(responseWriter, "Register this device before reporting chunks", http.StatusForbidden)
		return
	}
	// Return an internal error when the registration query fails.
	if deviceRegistrationQueryError != nil {
		http.Error(responseWriter, "Cannot verify registered device", http.StatusInternalServerError)
		return
	}

	// Replace the device's complete chunk inventory in one operation.
	chunkInventorySavingError := registry.chunkLocations.ReplaceDeviceChunks(request.Context(), registeredDeviceID, chunkHashes)
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
