// Author: Rabindra Neupane
package network

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rabindra Neupane
// DeviceSettings stores this installation's persistent identity and coordinator choice.
type DeviceSettings struct {
	RegistrationKey    string `json:"registrationKey"`
	DeviceID           string `json:"deviceId"`
	Coordinator        bool   `json:"coordinator"`
	CoordinatorAddress string `json:"coordinatorAddress"`
}

// Rabindra Neupane
// RegistrationStatus exposes public registry state without the private key.
type RegistrationStatus struct {
	DeviceID           string `json:"deviceId"`
	DeviceName         string `json:"deviceName"`
	LocalAddress       string `json:"localAddress"`
	Coordinator        bool   `json:"coordinator"`
	CoordinatorAddress string `json:"coordinatorAddress"`
	Configured         bool   `json:"configured"`
	Registered         bool   `json:"registered"`
	LastError          string `json:"lastError"`
}

// Rabindra Neupane
// registrationRequest is the private registration message exchanged with the
// coordinator. The registration key identifies one FileSync installation.
type registrationRequest struct {
	RegistrationKey string `json:"registrationKey"`
	DeviceID        string `json:"deviceId"`
	Name            string `json:"name"`
	Port            int    `json:"port"`
}

// Rabindra Neupane
// DeviceRegistry owns persistent identity, coordinator settings, heartbeat
// state, registered-device queries, and the optional chunk-location service.
type DeviceRegistry struct {
	chunkLocations ChunkLocationStore
	mutex          sync.Mutex
	database       *sql.DB
	settings       DeviceSettings
	settingsPath   string
	name           string
	port           int
	localAddress   string
	registered     bool
	lastError      string
	generation     uint64
	started        int64
	serviceContext context.Context
	cancelService  context.CancelFunc
	wakeSignal     chan struct{}
	doneSignal     chan struct{}
}

// Rabindra Neupane
// newDeviceToken creates a random lowercase identifier used for registration
// keys and coordinator-assigned device IDs.
func newDeviceToken() (string, error) {
	// Read secure random bytes for a registration key or device ID.
	var token [16]byte
	if _, tokenGenerationError := rand.Read(token[:]); tokenGenerationError != nil {
		return "", tokenGenerationError
	}
	return hex.EncodeToString(token[:]), nil
}

// Rabindra Neupane
// OpenDeviceRegistry loads or creates this installation's identity, validates
// saved coordinator settings, and starts the heartbeat service.
func OpenDeviceRegistry(applicationContext context.Context, database *sql.DB, projectRoot string, serverPort int) (*DeviceRegistry, error) {
	// Build the path used to preserve this installation's private identity.
	settingsPath := filepath.Join(projectRoot, ".filesync-device.json")
	deviceSettings := DeviceSettings{}
	// Read any identity and coordinator settings saved by an earlier run.
	settingsData, settingsReadingError := os.ReadFile(settingsPath)
	// Decode the saved settings when the file exists.
	if settingsReadingError == nil {
		// Stop when the settings file does not contain valid JSON.
		if settingsDecodingError := json.Unmarshal(settingsData, &deviceSettings); settingsDecodingError != nil {
			return nil, fmt.Errorf("read device settings: %w", settingsDecodingError)
		}
	} else if !os.IsNotExist(settingsReadingError) {
		return nil, settingsReadingError
	}
	// Create a registration key for a new FileSync installation.
	if deviceSettings.RegistrationKey == "" {
		var tokenGenerationError error
		deviceSettings.RegistrationKey, tokenGenerationError = newDeviceToken()
		if tokenGenerationError != nil {
			return nil, tokenGenerationError
		}
	}
	// Decode and validate the saved registration key.
	decodedKey, registrationKeyDecodingError := hex.DecodeString(deviceSettings.RegistrationKey)
	if registrationKeyDecodingError != nil || len(decodedKey) != 16 {
		return nil, fmt.Errorf("invalid device registration key in %s", settingsPath)
	}
	// Validate a saved remote coordinator address before using it.
	if deviceSettings.CoordinatorAddress != "" {
		if addressValidationError := validateCoordinatorAddress(deviceSettings.CoordinatorAddress); addressValidationError != nil {
			return nil, addressValidationError
		}
	}
	// Read the computer name shown to other registered devices.
	deviceName, deviceHostnameReadingError := os.Hostname()
	if deviceHostnameReadingError != nil {
		return nil, deviceHostnameReadingError
	}
	// Create a cancellable context for the background registration service.
	serviceContext, cancelService := context.WithCancel(applicationContext)
	// Create the registry with its saved identity and runtime channels.
	registry := &DeviceRegistry{database: database, settings: deviceSettings, settingsPath: settingsPath, name: deviceName, port: serverPort, started: time.Now().UnixNano(), serviceContext: serviceContext, cancelService: cancelService, wakeSignal: make(chan struct{}, 1), doneSignal: make(chan struct{})}
	// Save the complete settings so a new key survives future restarts.
	if settingsSavingError := registry.saveSettingsLocked(deviceSettings); settingsSavingError != nil {
		cancelService()
		return nil, settingsSavingError
	}
	// Start registration and heartbeat work in the background.
	go registry.run()
	// Return the ready registry to the application.
	return registry, nil
}

// Rabindra Neupane
// saveSettingsLocked writes settings through a temporary file before renaming
// it, preventing a partial write from corrupting the saved device identity.
func (registry *DeviceRegistry) saveSettingsLocked(settings DeviceSettings) error {
	// Create readable JSON for the local settings file.
	settingsData, settingsEncodingError := json.MarshalIndent(settings, "", "  ")
	if settingsEncodingError != nil {
		return settingsEncodingError
	}
	// Create a temporary file beside the final settings file.
	temporaryFile, settingsTemporaryFileCreationError := os.CreateTemp(filepath.Dir(registry.settingsPath), ".filesync-device-*.tmp")
	if settingsTemporaryFileCreationError != nil {
		return settingsTemporaryFileCreationError
	}
	// Remove the temporary file if any later step fails.
	defer os.Remove(temporaryFile.Name())
	// Write the settings and close the temporary file.
	_, settingsWritingError := temporaryFile.Write(settingsData)
	temporaryFileClosingError := temporaryFile.Close()
	// Return a writing error before replacing the saved settings.
	if settingsWritingError != nil {
		return settingsWritingError
	}
	// Return a closing error because buffered data may not be complete.
	if temporaryFileClosingError != nil {
		return temporaryFileClosingError
	}
	// Atomically replace the old settings file with the completed file.
	return os.Rename(temporaryFile.Name(), registry.settingsPath)
}

// Rabindra Neupane
// validateCoordinatorAddress accepts one host and TCP port and rejects paths,
// credentials, whitespace, and invalid port numbers.
func validateCoordinatorAddress(coordinatorAddress string) error {
	// Split the address and reject unsafe host text or missing values.
	hostName, portText, coordinatorAddressParsingError := net.SplitHostPort(coordinatorAddress)
	if coordinatorAddressParsingError != nil || hostName == "" || strings.ContainsAny(hostName, "/\\?#@ \t\r\n") {
		return fmt.Errorf("enter a coordinator address as IP:port, for example 192.168.1.10:8080")
	}
	// Convert the port text and check the valid TCP port range.
	portNumber, coordinatorPortParsingError := strconv.Atoi(portText)
	if coordinatorPortParsingError != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("invalid coordinator port")
	}
	// Return success after both host and port pass validation.
	return nil
}

// Rabindra Neupane
// Configure switches coordinator mode or selects a remote coordinator. Changing
// coordinators clears the old assigned ID so the new coordinator can register it.
func (registry *DeviceRegistry) Configure(isCoordinator bool, coordinatorAddress string) error {
	// Normalize the value entered by the user.
	coordinatorAddress = strings.TrimSpace(coordinatorAddress)
	// Clear the address in coordinator mode or validate a remote address.
	if isCoordinator {
		coordinatorAddress = ""
	} else if coordinatorAddressValidationError := validateCoordinatorAddress(coordinatorAddress); coordinatorAddressValidationError != nil {
		return coordinatorAddressValidationError
	}
	// Lock the registry while changing and saving its configuration.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	// Copy the current settings before applying the requested changes.
	updatedSettings := registry.settings
	// Clear the assigned ID when switching to a different coordinator.
	if isCoordinator != updatedSettings.Coordinator || coordinatorAddress != updatedSettings.CoordinatorAddress {
		updatedSettings.DeviceID = ""
	}
	// Store the selected role and coordinator address in the copied settings.
	updatedSettings.Coordinator, updatedSettings.CoordinatorAddress = isCoordinator, coordinatorAddress
	// Save the updated settings before exposing them to the running service.
	if settingsSavingError := registry.saveSettingsLocked(updatedSettings); settingsSavingError != nil {
		return settingsSavingError
	}
	// Reset registration state so the background service registers again.
	registry.settings = updatedSettings
	registry.generation++
	registry.registered = false
	registry.lastError = "Registering..."
	// Wake the background service without blocking if it is already awake.
	select {
	case registry.wakeSignal <- struct{}{}:
	default:
	}
	// Return after the new configuration is saved and queued.
	return nil
}

// Rabindra Neupane
// Status returns a thread-safe public snapshot without exposing the private
// registration key.
func (registry *DeviceRegistry) Status() RegistrationStatus {
	// Lock settings while creating a consistent public snapshot.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	coordinatorAddress := registry.settings.CoordinatorAddress
	// Show this device's address when it is acting as coordinator.
	if registry.settings.Coordinator {
		coordinatorAddress = registry.localAddress
	}
	// Return a public copy without the private registration key.
	return RegistrationStatus{DeviceID: registry.settings.DeviceID, DeviceName: registry.name, LocalAddress: registry.localAddress, Coordinator: registry.settings.Coordinator, CoordinatorAddress: coordinatorAddress, Configured: registry.settings.Coordinator || coordinatorAddress != "", Registered: registry.registered, LastError: registry.lastError}
}

// Rabindra Neupane
// Close stops the heartbeat service and waits for its goroutine to finish.
func (registry *DeviceRegistry) Close() { /* Stop the service and wait for its completion signal. */ registry.cancelService(); <-registry.doneSignal }

// Rabindra Neupane
// Handler groups the coordinator's registration, heartbeat, and device-list routes.
func (registry *DeviceRegistry) Handler() http.Handler {
	// Create one router for registration, heartbeat, and device-list requests.
	deviceRoutes := http.NewServeMux()
	// Route initial registrations through the shared registration handler.
	deviceRoutes.HandleFunc("/devices/register", func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
		registry.registrationHandler(responseWriter, httpRequest, false)
	})
	// Route heartbeat updates through the same handler with heartbeat checks enabled.
	deviceRoutes.HandleFunc("/devices/heartbeat", func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
		registry.registrationHandler(responseWriter, httpRequest, true)
	})
	// Route device-list reads to the coordinator device handler.
	deviceRoutes.HandleFunc("/devices", registry.devicesHandler)
	// Return the completed device registry router.
	return deviceRoutes
}

// Rabindra Neupane
// registrationHandler validates a coordinator request and derives the peer IP
// from the connection instead of trusting an address supplied in JSON.
func (registry *DeviceRegistry) registrationHandler(responseWriter http.ResponseWriter, httpRequest *http.Request, heartbeat bool) {
	// Validate that registration updates use POST.
	if httpRequest.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", "POST")
		http.Error(responseWriter, "Only POST requests are allowed", 405)
		return
	}
	// Decode one strict registration request from the device.
	var registration registrationRequest
	if registrationRequestReadingError := readTransferJSON(responseWriter, httpRequest, &registration); registrationRequestReadingError != nil {
		http.Error(responseWriter, "Invalid registration request", 400)
		return
	}
	// Decode and validate identity, name, and port fields.
	registrationKey, registrationKeyDecodingError := hex.DecodeString(registration.RegistrationKey)
	if registrationKeyDecodingError != nil || len(registrationKey) != 16 || registration.Name == "" || len(registration.Name) > 255 || registration.Port < 1 || registration.Port > 65535 {
		http.Error(responseWriter, "Invalid device registration fields", 400)
		return
	}
	// Read the peer IP from the network connection instead of trusting JSON.
	deviceIP, _, remoteAddressParsingError := net.SplitHostPort(httpRequest.RemoteAddr)
	if remoteAddressParsingError != nil {
		http.Error(responseWriter, "Cannot determine peer IP", 400)
		return
	}
	// Create or refresh the registered device in coordinator storage.
	registeredDevice, deviceRegistrationError := registry.acceptRegistration(httpRequest.Context(), registration, deviceIP, heartbeat)
	if deviceRegistrationError != nil {
		http.Error(responseWriter, deviceRegistrationError.Error(), http.StatusServiceUnavailable)
		return
	}
	// Respond with the stable identity assigned to the device.
	writeTransferJSON(responseWriter, registeredDevice)
}

// Rabindra Neupane
// acceptRegistration creates a stable device ID for a new installation or
// refreshes the stored name, address, port, and last-seen time for a known one.
func (registry *DeviceRegistry) acceptRegistration(requestContext context.Context, registration registrationRequest, deviceIP string, heartbeat bool) (Device, error) {
	// Lock coordinator state and database registration updates together.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	// Reject registration work when this installation is not coordinator.
	if !registry.settings.Coordinator {
		return Device{}, fmt.Errorf("this device is not the coordinator")
	}
	// Look up an existing device by its private registration key.
	var deviceID string
	registrationLookupError := registry.database.QueryRowContext(requestContext, "SELECT device_id FROM device_registry WHERE registration_key=?", registration.RegistrationKey).Scan(&deviceID)
	// Reject a heartbeat when its device is unknown or uses the wrong ID.
	if heartbeat && (registrationLookupError == sql.ErrNoRows || (registrationLookupError == nil && deviceID != registration.DeviceID)) {
		return Device{}, fmt.Errorf("device must register before sending heartbeats")
	}
	// Return unexpected database lookup errors.
	if registrationLookupError != nil && registrationLookupError != sql.ErrNoRows {
		return Device{}, registrationLookupError
	}
	// Create a stable device ID for a new registration key.
	if registrationLookupError == sql.ErrNoRows {
		var tokenGenerationError error
		deviceID, tokenGenerationError = newDeviceToken()
		if tokenGenerationError != nil {
			return Device{}, tokenGenerationError
		}
	}
	// Record the current time for online and offline tracking.
	lastSeenTime := time.Now().UnixNano()
	// Insert a new registration or refresh the existing device details.
	_, registrationSavingError := registry.database.ExecContext(requestContext, `INSERT INTO device_registry (registration_key,device_id,name,ip,port,last_seen)
		VALUES (?,?,?,?,?,?) ON CONFLICT(registration_key) DO UPDATE SET name=excluded.name,
		ip=excluded.ip,port=excluded.port,last_seen=excluded.last_seen`, registration.RegistrationKey, deviceID, registration.Name, deviceIP, registration.Port, lastSeenTime)
	if registrationSavingError != nil {
		return Device{}, registrationSavingError
	}
	// Return the registered device with its current online state.
	return Device{DeviceID: deviceID, DeviceName: registration.Name, DeviceIP: deviceIP, DevicePort: registration.Port, DeviceOnline: true, LastSeen: time.Unix(0, lastSeenTime).UTC().Format(time.RFC3339)}, nil
}

// Rabindra Neupane
// devicesHandler returns the coordinator's current registered-device view.
func (registry *DeviceRegistry) devicesHandler(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	// Validate that device-list requests use GET.
	if httpRequest.Method != http.MethodGet {
		responseWriter.Header().Set("Allow", "GET")
		http.Error(responseWriter, "Only GET requests are allowed", 405)
		return
	}
	// Read the latest registered-device list from coordinator storage.
	registeredDevices, deviceRegistryReadingError := registry.readRegistry(httpRequest.Context())
	if deviceRegistryReadingError != nil {
		http.Error(responseWriter, deviceRegistryReadingError.Error(), 503)
		return
	}
	// Respond with the current device list.
	writeTransferJSON(responseWriter, registeredDevices)
}

// Rabindra Neupane
// readRegistry builds the device list and calculates availability from recent
// heartbeats. Entries from a previous coordinator run remain visible but offline.
func (registry *DeviceRegistry) readRegistry(requestContext context.Context) ([]Device, error) {
	// Lock coordinator state while reading and classifying registrations.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	// Reject local registry reads when this device is not coordinator.
	if !registry.settings.Coordinator {
		return nil, fmt.Errorf("this device is not the coordinator")
	}
	// Query all saved devices in a stable display order.
	deviceRows, deviceRegistryQueryError := registry.database.QueryContext(requestContext, "SELECT device_id,name,ip,port,last_seen FROM device_registry ORDER BY name,device_id")
	if deviceRegistryQueryError != nil {
		return nil, deviceRegistryQueryError
	}
	// Close the database rows after the device list is built.
	defer deviceRows.Close()
	// Create the result list and calculate the online cutoff.
	registeredDevices := make([]Device, 0)
	// Calculate the cutoff used to mark devices offline after missed heartbeats.
	offlineCutoff := time.Now().Add(-15 * time.Second).UnixNano()
	// Read and classify every saved device record.
	for deviceRows.Next() {
		// Scan one database row into a public device record.
		var device Device
		var lastSeenTime int64
		if deviceRowScanningError := deviceRows.Scan(&device.DeviceID, &device.DeviceName, &device.DeviceIP, &device.DevicePort, &lastSeenTime); deviceRowScanningError != nil {
			return nil, deviceRowScanningError
		}
		// Calculate availability and coordinator role from saved timestamps and IDs.
		device.DeviceOnline = lastSeenTime >= offlineCutoff && lastSeenTime >= registry.started
		device.LastSeen = time.Unix(0, lastSeenTime).UTC().Format(time.RFC3339)
		device.Coordinator = device.DeviceID == registry.settings.DeviceID
		registeredDevices = append(registeredDevices, device)
	}
	// Return the device list and any final database iteration error.
	return registeredDevices, deviceRows.Err()
}

// Rabindra Neupane
// Devices reads locally when this installation is the coordinator; otherwise it
// asks the configured coordinator for the same device list.
func (registry *DeviceRegistry) Devices() ([]Device, error) {
	// Read the current role and coordinator address.
	registrationStatus := registry.Status()
	// Limit the local or remote device-list request.
	requestContext, cancelRequest := context.WithTimeout(registry.serviceContext, 5*time.Second)
	defer cancelRequest()
	// Read directly from storage when this device is coordinator.
	if registrationStatus.Coordinator {
		return registry.readRegistry(requestContext)
	}
	// Require a coordinator selection before making a remote request.
	if !registrationStatus.Configured {
		return nil, fmt.Errorf("select a coordinator first")
	}
	// Ask the selected coordinator for its registered-device list.
	var registeredDevices []Device
	deviceListRequestError := registry.registryRequest(requestContext, registrationStatus.CoordinatorAddress, "/devices", nil, &registeredDevices)
	if deviceListRequestError != nil {
		return nil, deviceListRequestError
	}
	// Return an empty list instead of a nil value when no devices exist.
	if registeredDevices == nil {
		registeredDevices = make([]Device, 0)
	}
	// Return the coordinator's validated device list.
	return registeredDevices, nil
}
