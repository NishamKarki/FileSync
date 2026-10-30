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

// DeviceSettings stores this installation's registration and coordinator settings.
// Rabindra Neupane
// Registration, coordinator handlers, persistence and heartbeats are kept here.
type DeviceSettings struct {
	RegistrationKey    string `json:"registrationKey"`
	DeviceID           string `json:"deviceId"`
	Coordinator        bool   `json:"coordinator"`
	CoordinatorAddress string `json:"coordinatorAddress"`
}

// Rabindra Neupane
// Status deliberately omits the installation's private registration key.
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
type registrationRequest struct {
	RegistrationKey string `json:"registrationKey"`
	DeviceID        string `json:"deviceId"`
	Name            string `json:"name"`
	Port            int    `json:"port"`
}

// Rabindra Neupane
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
func newDeviceToken() (string, error) {
	var token [16]byte
	if _, tokenGenerationError := rand.Read(token[:]); tokenGenerationError != nil {
		return "", tokenGenerationError
	}
	return hex.EncodeToString(token[:]), nil
}

// Rabindra Neupane
func OpenDeviceRegistry(applicationContext context.Context, database *sql.DB, projectRoot string, serverPort int) (*DeviceRegistry, error) {
	settingsPath := filepath.Join(projectRoot, ".filesync-device.json")
	deviceSettings := DeviceSettings{}
	settingsData, settingsReadingError := os.ReadFile(settingsPath)
	if settingsReadingError == nil {
		if settingsDecodingError := json.Unmarshal(settingsData, &deviceSettings); settingsDecodingError != nil {
			return nil, fmt.Errorf("read device settings: %w", settingsDecodingError)
		}
	} else if !os.IsNotExist(settingsReadingError) {
		return nil, settingsReadingError
	}
	if deviceSettings.RegistrationKey == "" {
		var tokenGenerationError error
		deviceSettings.RegistrationKey, tokenGenerationError = newDeviceToken()
		if tokenGenerationError != nil {
			return nil, tokenGenerationError
		}
	}
	decodedKey, registrationKeyDecodingError := hex.DecodeString(deviceSettings.RegistrationKey)
	if registrationKeyDecodingError != nil || len(decodedKey) != 16 {
		return nil, fmt.Errorf("invalid device registration key in %s", settingsPath)
	}
	if deviceSettings.CoordinatorAddress != "" {
		if addressValidationError := validateCoordinatorAddress(deviceSettings.CoordinatorAddress); addressValidationError != nil {
			return nil, addressValidationError
		}
	}
	deviceName, deviceHostnameReadingError := os.Hostname()
	if deviceHostnameReadingError != nil {
		return nil, deviceHostnameReadingError
	}
	serviceContext, cancelService := context.WithCancel(applicationContext)
	registry := &DeviceRegistry{database: database, settings: deviceSettings, settingsPath: settingsPath, name: deviceName, port: serverPort, started: time.Now().UnixNano(), serviceContext: serviceContext, cancelService: cancelService, wakeSignal: make(chan struct{}, 1), doneSignal: make(chan struct{})}
	if settingsSavingError := registry.saveSettingsLocked(deviceSettings); settingsSavingError != nil {
		cancelService()
		return nil, settingsSavingError
	}
	go registry.run()
	return registry, nil
}

// Rabindra Neupane
func (registry *DeviceRegistry) saveSettingsLocked(settings DeviceSettings) error {
	settingsData, settingsEncodingError := json.MarshalIndent(settings, "", "  ")
	if settingsEncodingError != nil {
		return settingsEncodingError
	}
	temporaryFile, settingsTemporaryFileCreationError := os.CreateTemp(filepath.Dir(registry.settingsPath), ".filesync-device-*.tmp")
	if settingsTemporaryFileCreationError != nil {
		return settingsTemporaryFileCreationError
	}
	defer os.Remove(temporaryFile.Name())
	_, settingsWritingError := temporaryFile.Write(settingsData)
	temporaryFileClosingError := temporaryFile.Close()
	if settingsWritingError != nil {
		return settingsWritingError
	}
	if temporaryFileClosingError != nil {
		return temporaryFileClosingError
	}
	return os.Rename(temporaryFile.Name(), registry.settingsPath)
}

// Rabindra Neupane
func validateCoordinatorAddress(coordinatorAddress string) error {
	hostName, portText, coordinatorAddressParsingError := net.SplitHostPort(coordinatorAddress)
	if coordinatorAddressParsingError != nil || hostName == "" || strings.ContainsAny(hostName, "/\\?#@ \t\r\n") {
		return fmt.Errorf("enter a coordinator address as IP:port, for example 192.168.1.10:8080")
	}
	portNumber, coordinatorPortParsingError := strconv.Atoi(portText)
	if coordinatorPortParsingError != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("invalid coordinator port")
	}
	return nil
}

// Rabindra Neupane
func (registry *DeviceRegistry) Configure(isCoordinator bool, coordinatorAddress string) error {
	coordinatorAddress = strings.TrimSpace(coordinatorAddress)
	if isCoordinator {
		coordinatorAddress = ""
	} else if coordinatorAddressValidationError := validateCoordinatorAddress(coordinatorAddress); coordinatorAddressValidationError != nil {
		return coordinatorAddressValidationError
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	updatedSettings := registry.settings
	if isCoordinator != updatedSettings.Coordinator || coordinatorAddress != updatedSettings.CoordinatorAddress {
		updatedSettings.DeviceID = ""
	}
	updatedSettings.Coordinator, updatedSettings.CoordinatorAddress = isCoordinator, coordinatorAddress
	if settingsSavingError := registry.saveSettingsLocked(updatedSettings); settingsSavingError != nil {
		return settingsSavingError
	}
	registry.settings = updatedSettings
	registry.generation++
	registry.registered = false
	registry.lastError = "Registering..."
	select {
	case registry.wakeSignal <- struct{}{}:
	default:
	}
	return nil
}

// Rabindra Neupane
func (registry *DeviceRegistry) Status() RegistrationStatus {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	coordinatorAddress := registry.settings.CoordinatorAddress
	if registry.settings.Coordinator {
		coordinatorAddress = registry.localAddress
	}
	return RegistrationStatus{DeviceID: registry.settings.DeviceID, DeviceName: registry.name, LocalAddress: registry.localAddress, Coordinator: registry.settings.Coordinator, CoordinatorAddress: coordinatorAddress, Configured: registry.settings.Coordinator || coordinatorAddress != "", Registered: registry.registered, LastError: registry.lastError}
}

// Rabindra Neupane
func (registry *DeviceRegistry) Close() { registry.cancelService(); <-registry.doneSignal }

// Rabindra Neupane
func (registry *DeviceRegistry) Handler() http.Handler {
	deviceRoutes := http.NewServeMux()
	deviceRoutes.HandleFunc("/devices/register", func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
		registry.registrationHandler(responseWriter, httpRequest, false)
	})
	deviceRoutes.HandleFunc("/devices/heartbeat", func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
		registry.registrationHandler(responseWriter, httpRequest, true)
	})
	deviceRoutes.HandleFunc("/devices", registry.devicesHandler)
	return deviceRoutes
}

// Rabindra Neupane
func (registry *DeviceRegistry) registrationHandler(responseWriter http.ResponseWriter, httpRequest *http.Request, heartbeat bool) {
	if httpRequest.Method != http.MethodPost {
		responseWriter.Header().Set("Allow", "POST")
		http.Error(responseWriter, "Only POST requests are allowed", 405)
		return
	}
	var registration registrationRequest
	if registrationRequestReadingError := readTransferJSON(responseWriter, httpRequest, &registration); registrationRequestReadingError != nil {
		http.Error(responseWriter, "Invalid registration request", 400)
		return
	}
	registrationKey, registrationKeyDecodingError := hex.DecodeString(registration.RegistrationKey)
	if registrationKeyDecodingError != nil || len(registrationKey) != 16 || registration.Name == "" || len(registration.Name) > 255 || registration.Port < 1 || registration.Port > 65535 {
		http.Error(responseWriter, "Invalid device registration fields", 400)
		return
	}
	deviceIP, _, remoteAddressParsingError := net.SplitHostPort(httpRequest.RemoteAddr)
	if remoteAddressParsingError != nil {
		http.Error(responseWriter, "Cannot determine peer IP", 400)
		return
	}
	registeredDevice, deviceRegistrationError := registry.acceptRegistration(httpRequest.Context(), registration, deviceIP, heartbeat)
	if deviceRegistrationError != nil {
		http.Error(responseWriter, deviceRegistrationError.Error(), http.StatusServiceUnavailable)
		return
	}
	writeTransferJSON(responseWriter, registeredDevice)
}

// Rabindra Neupane
func (registry *DeviceRegistry) acceptRegistration(requestContext context.Context, registration registrationRequest, deviceIP string, heartbeat bool) (Device, error) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.settings.Coordinator {
		return Device{}, fmt.Errorf("this device is not the coordinator")
	}
	var deviceID string
	registrationLookupError := registry.database.QueryRowContext(requestContext, "SELECT device_id FROM device_registry WHERE registration_key=?", registration.RegistrationKey).Scan(&deviceID)
	if heartbeat && (registrationLookupError == sql.ErrNoRows || (registrationLookupError == nil && deviceID != registration.DeviceID)) {
		return Device{}, fmt.Errorf("device must register before sending heartbeats")
	}
	if registrationLookupError != nil && registrationLookupError != sql.ErrNoRows {
		return Device{}, registrationLookupError
	}
	if registrationLookupError == sql.ErrNoRows {
		var tokenGenerationError error
		deviceID, tokenGenerationError = newDeviceToken()
		if tokenGenerationError != nil {
			return Device{}, tokenGenerationError
		}
	}
	lastSeenTime := time.Now().UnixNano()
	_, registrationSavingError := registry.database.ExecContext(requestContext, `INSERT INTO device_registry (registration_key,device_id,name,ip,port,last_seen)
		VALUES (?,?,?,?,?,?) ON CONFLICT(registration_key) DO UPDATE SET name=excluded.name,
		ip=excluded.ip,port=excluded.port,last_seen=excluded.last_seen`, registration.RegistrationKey, deviceID, registration.Name, deviceIP, registration.Port, lastSeenTime)
	if registrationSavingError != nil {
		return Device{}, registrationSavingError
	}
	return Device{DeviceID: deviceID, DeviceName: registration.Name, DeviceIP: deviceIP, DevicePort: registration.Port, DeviceOnline: true, LastSeen: time.Unix(0, lastSeenTime).UTC().Format(time.RFC3339)}, nil
}

// Rabindra Neupane
func (registry *DeviceRegistry) devicesHandler(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	if httpRequest.Method != http.MethodGet {
		responseWriter.Header().Set("Allow", "GET")
		http.Error(responseWriter, "Only GET requests are allowed", 405)
		return
	}
	registeredDevices, deviceRegistryReadingError := registry.readRegistry(httpRequest.Context())
	if deviceRegistryReadingError != nil {
		http.Error(responseWriter, deviceRegistryReadingError.Error(), 503)
		return
	}
	writeTransferJSON(responseWriter, registeredDevices)
}

// Rabindra Neupane
func (registry *DeviceRegistry) readRegistry(requestContext context.Context) ([]Device, error) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.settings.Coordinator {
		return nil, fmt.Errorf("this device is not the coordinator")
	}
	deviceRows, deviceRegistryQueryError := registry.database.QueryContext(requestContext, "SELECT device_id,name,ip,port,last_seen FROM device_registry ORDER BY name,device_id")
	if deviceRegistryQueryError != nil {
		return nil, deviceRegistryQueryError
	}
	defer deviceRows.Close()
	registeredDevices := make([]Device, 0)
	// Mark devices offline after 15 seconds without a heartbeat.
	offlineCutoff := time.Now().Add(-15 * time.Second).UnixNano()
	for deviceRows.Next() {
		var device Device
		var lastSeenTime int64
		if deviceRowScanningError := deviceRows.Scan(&device.DeviceID, &device.DeviceName, &device.DeviceIP, &device.DevicePort, &lastSeenTime); deviceRowScanningError != nil {
			return nil, deviceRowScanningError
		}
		device.DeviceOnline = lastSeenTime >= offlineCutoff && lastSeenTime >= registry.started
		device.LastSeen = time.Unix(0, lastSeenTime).UTC().Format(time.RFC3339)
		device.Coordinator = device.DeviceID == registry.settings.DeviceID
		registeredDevices = append(registeredDevices, device)
	}
	return registeredDevices, deviceRows.Err()
}

// Rabindra Neupane
func (registry *DeviceRegistry) Devices() ([]Device, error) {
	registrationStatus := registry.Status()
	requestContext, cancelRequest := context.WithTimeout(registry.serviceContext, 5*time.Second)
	defer cancelRequest()
	if registrationStatus.Coordinator {
		return registry.readRegistry(requestContext)
	}
	if !registrationStatus.Configured {
		return nil, fmt.Errorf("select a coordinator first")
	}
	var registeredDevices []Device
	deviceListRequestError := registry.registryRequest(requestContext, registrationStatus.CoordinatorAddress, "/devices", nil, &registeredDevices)
	if deviceListRequestError != nil {
		return nil, deviceListRequestError
	}
	if registeredDevices == nil {
		registeredDevices = make([]Device, 0)
	}
	return registeredDevices, nil
}
