///Rabindra Neupane

package network

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
//Rabindra Neupane
// Registration, coordinator handlers, persistence and heartbeats are kept here.
type DeviceSettings struct {
	RegistrationKey    string `json:"registrationKey"`
	DeviceID           string `json:"deviceId"`
	Coordinator        bool   `json:"coordinator"`
	CoordinatorAddress string `json:"coordinatorAddress"`
}

//Rabindra Neupane
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

//Rabindra Neupane
type registrationRequest struct {
	RegistrationKey string `json:"registrationKey"`
	DeviceID        string `json:"deviceId"`
	Name            string `json:"name"`
	Port            int    `json:"port"`
}

//Rabindra Neupane
type DeviceRegistry struct {
	//Rabindra Neupane
	chunkLocations ChunkLocationStore
	mutex          sync.Mutex
	db             *sql.DB
	settings       DeviceSettings
	settingsPath   string
	name           string
	port           int
	localAddress   string
	registered     bool
	lastError      string
	generation     uint64
	started        int64
	ctx            context.Context
	cancel         context.CancelFunc
	wake           chan struct{}
	done           chan struct{}
}

//Rabindra Neupane
func newDeviceToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}

//Rabindra Neupane
func OpenDeviceRegistry(ctx context.Context, db *sql.DB, projectRoot string, port int) (*DeviceRegistry, error) {
	path := filepath.Join(projectRoot, ".filesync-device.json")
	settings := DeviceSettings{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("read device settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if settings.RegistrationKey == "" {
		settings.RegistrationKey, err = newDeviceToken()
		if err != nil {
			return nil, err
		}
	}
	if decoded, err := hex.DecodeString(settings.RegistrationKey); err != nil || len(decoded) != 16 {
		return nil, fmt.Errorf("invalid device registration key in %s", path)
	}
	if settings.CoordinatorAddress != "" {
		if err := validateCoordinatorAddress(settings.CoordinatorAddress); err != nil {
			return nil, err
		}
	}
	name, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	registry := &DeviceRegistry{db: db, settings: settings, settingsPath: path, name: name, port: port, started: time.Now().UnixNano(), ctx: serviceCtx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	if err := registry.saveSettingsLocked(settings); err != nil {
		cancel()
		return nil, err
	}
	go registry.run()
	return registry, nil
}

//Rabindra Neupane
func (registry *DeviceRegistry) saveSettingsLocked(settings DeviceSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(registry.settingsPath), ".filesync-device-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), registry.settingsPath)
}

//Rabindra Neupane
func validateCoordinatorAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" || strings.ContainsAny(host, "/\\?#@ \t\r\n") {
		return fmt.Errorf("enter a coordinator address as IP:port, for example 192.168.1.10:8080")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid coordinator port")
	}
	return nil
}

//Rabindra Neupane
func (registry *DeviceRegistry) Configure(coordinator bool, address string) error {
	address = strings.TrimSpace(address)
	if coordinator {
		address = ""
	} else if err := validateCoordinatorAddress(address); err != nil {
		return err
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	settings := registry.settings
	if coordinator != settings.Coordinator || address != settings.CoordinatorAddress {
		settings.DeviceID = ""
	}
	settings.Coordinator, settings.CoordinatorAddress = coordinator, address
	if err := registry.saveSettingsLocked(settings); err != nil {
		return err
	}
	registry.settings = settings
	registry.generation++
	registry.registered = false
	registry.lastError = "Registering..."
	select {
	case registry.wake <- struct{}{}:
	default:
	}
	return nil
}

//Rabindra Neupane
func (registry *DeviceRegistry) Status() RegistrationStatus {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	address := registry.settings.CoordinatorAddress
	if registry.settings.Coordinator {
		address = registry.localAddress
	}
	return RegistrationStatus{DeviceID: registry.settings.DeviceID, DeviceName: registry.name, LocalAddress: registry.localAddress, Coordinator: registry.settings.Coordinator, CoordinatorAddress: address, Configured: registry.settings.Coordinator || address != "", Registered: registry.registered, LastError: registry.lastError}
}

//Rabindra Neupane
func (registry *DeviceRegistry) Close() { registry.cancel(); <-registry.done }

//Rabindra Neupane
func (registry *DeviceRegistry) run() {
	defer close(registry.done)
	//Rabindra Neupane
	// Send heartbeats often enough for the shorter offline timeout.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		registry.sendHeartbeat()
		select {
		case <-registry.ctx.Done():
			return
		case <-ticker.C:
		case <-registry.wake:
		}
	}
}

//Rabindra Neupane
func (registry *DeviceRegistry) sendHeartbeat() {
	registry.mutex.Lock()
	settings, generation := registry.settings, registry.generation
	registry.mutex.Unlock()
	if !settings.Coordinator && settings.CoordinatorAddress == "" {
		return
	}
	ctx, cancel := context.WithTimeout(registry.ctx, 5*time.Second)
	defer cancel()
	ip, err := GetLocalIP()
	var device Device
	if err == nil {
		request := registrationRequest{RegistrationKey: settings.RegistrationKey, DeviceID: settings.DeviceID, Name: registry.name, Port: registry.port}
		if settings.Coordinator {
			device, err = registry.acceptRegistration(ctx, request, ip, false)
		} else {
			path := "/devices/register"
			if request.DeviceID != "" {
				path = "/devices/heartbeat"
			}
			err = registry.registryRequest(ctx, settings.CoordinatorAddress, path, request, &device)
			if err != nil && path == "/devices/heartbeat" {
				// Registration is idempotent by installation key, including after
				// a coordinator restart or a previously lost registration response.
				err = registry.registryRequest(ctx, settings.CoordinatorAddress, "/devices/register", request, &device)
			}
		}
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if generation != registry.generation {
		return
	}
	registry.localAddress = net.JoinHostPort(ip, strconv.Itoa(registry.port))
	if err == nil && device.DeviceID == "" {
		err = fmt.Errorf("coordinator returned an empty device ID")
	}
	if err == nil && device.DeviceID != registry.settings.DeviceID {
		next := registry.settings
		next.DeviceID = device.DeviceID
		if saveErr := registry.saveSettingsLocked(next); saveErr != nil {
			err = saveErr
		} else {
			registry.settings = next
		}
	}
	registry.registered = err == nil
	registry.lastError = ""
	if err != nil {
		registry.lastError = err.Error()
		fmt.Println("Device registration:", err)
	}
}

//Rabindra Neupane
func (registry *DeviceRegistry) registryRequest(ctx context.Context, address, path string, payload interface{}, result interface{}) error {
	method := http.MethodGet
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("coordinator unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("coordinator returned %s: %s", response.Status, strings.TrimSpace(string(detail)))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("coordinator response is too large")
	}
	return json.Unmarshal(data, result)
}

//Rabindra Neupane
func (registry *DeviceRegistry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/devices/register", func(w http.ResponseWriter, r *http.Request) { registry.registrationHandler(w, r, false) })
	mux.HandleFunc("/devices/heartbeat", func(w http.ResponseWriter, r *http.Request) { registry.registrationHandler(w, r, true) })
	mux.HandleFunc("/devices", registry.devicesHandler)
	return mux
}

//Rabindra Neupane
func (registry *DeviceRegistry) registrationHandler(w http.ResponseWriter, r *http.Request, heartbeat bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Only POST requests are allowed", 405)
		return
	}
	var request registrationRequest
	if err := readTransferJSON(w, r, &request); err != nil {
		http.Error(w, "Invalid registration request", 400)
		return
	}
	key, err := hex.DecodeString(request.RegistrationKey)
	if err != nil || len(key) != 16 || request.Name == "" || len(request.Name) > 255 || request.Port < 1 || request.Port > 65535 {
		http.Error(w, "Invalid device registration fields", 400)
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, "Cannot determine peer IP", 400)
		return
	}
	device, err := registry.acceptRegistration(r.Context(), request, ip, heartbeat)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeTransferJSON(w, device)
}

//Rabindra Neupane
func (registry *DeviceRegistry) acceptRegistration(ctx context.Context, request registrationRequest, ip string, heartbeat bool) (Device, error) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.settings.Coordinator {
		return Device{}, fmt.Errorf("this device is not the coordinator")
	}
	var id string
	err := registry.db.QueryRowContext(ctx, "SELECT device_id FROM device_registry WHERE registration_key=?", request.RegistrationKey).Scan(&id)
	if heartbeat && (err == sql.ErrNoRows || (err == nil && id != request.DeviceID)) {
		return Device{}, fmt.Errorf("device must register before sending heartbeats")
	}
	if err != nil && err != sql.ErrNoRows {
		return Device{}, err
	}
	if err == sql.ErrNoRows {
		id, err = newDeviceToken()
		if err != nil {
			return Device{}, err
		}
	}
	now := time.Now().UnixNano()
	_, err = registry.db.ExecContext(ctx, `INSERT INTO device_registry (registration_key,device_id,name,ip,port,last_seen)
		VALUES (?,?,?,?,?,?) ON CONFLICT(registration_key) DO UPDATE SET name=excluded.name,
		ip=excluded.ip,port=excluded.port,last_seen=excluded.last_seen`, request.RegistrationKey, id, request.Name, ip, request.Port, now)
	if err != nil {
		return Device{}, err
	}
	return Device{DeviceID: id, DeviceName: request.Name, DeviceIP: ip, DevicePort: request.Port, DeviceOnline: true, LastSeen: time.Unix(0, now).UTC().Format(time.RFC3339)}, nil
}

//Rabindra Neupane
func (registry *DeviceRegistry) devicesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Only GET requests are allowed", 405)
		return
	}
	devices, err := registry.readRegistry(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	writeTransferJSON(w, devices)
}

//Rabindra Neupane
func (registry *DeviceRegistry) readRegistry(ctx context.Context) ([]Device, error) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.settings.Coordinator {
		return nil, fmt.Errorf("this device is not the coordinator")
	}
	rows, err := registry.db.QueryContext(ctx, "SELECT device_id,name,ip,port,last_seen FROM device_registry ORDER BY name,device_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := make([]Device, 0)
	//Rabindra Neupane
	// Mark devices offline after 15 seconds without a heartbeat.
	cutoff := time.Now().Add(-15 * time.Second).UnixNano()
	for rows.Next() {
		var device Device
		var seen int64
		if err := rows.Scan(&device.DeviceID, &device.DeviceName, &device.DeviceIP, &device.DevicePort, &seen); err != nil {
			return nil, err
		}
		device.DeviceOnline = seen >= cutoff && seen >= registry.started
		device.LastSeen = time.Unix(0, seen).UTC().Format(time.RFC3339)
		device.Coordinator = device.DeviceID == registry.settings.DeviceID
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

//Rabindra Neupane
func (registry *DeviceRegistry) Devices() ([]Device, error) {
	status := registry.Status()
	ctx, cancel := context.WithTimeout(registry.ctx, 5*time.Second)
	defer cancel()
	if status.Coordinator {
		return registry.readRegistry(ctx)
	}
	if !status.Configured {
		return nil, fmt.Errorf("select a coordinator first")
	}
	var devices []Device
	if err := registry.registryRequest(ctx, status.CoordinatorAddress, "/devices", nil, &devices); err != nil {
		return nil, err
	}
	if devices == nil {
		devices = make([]Device, 0)
	}
	return devices, nil
}

//Rabindra Neupane
// SetChunkLocationStore connects the metadata implementation without owning its schema.
func (registry *DeviceRegistry) SetChunkLocationStore(store ChunkLocationStore) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.chunkLocations = store
}

//Rabindra Neupane
func validLocationHash(hash string) bool {
	decoded, err := hex.DecodeString(hash)
	return err == nil && len(decoded) == 32 && hash == strings.ToLower(hash)
}

//Rabindra Neupane
func (registry *DeviceRegistry) reportChunks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Only POST requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	var report ChunkReport
	if err := readTransferJSON(w, r, &report); err != nil || report.DeviceID == "" || report.RegistrationKey == "" || report.ChunkHashes == nil {
		http.Error(w, "Provide deviceId, registrationKey and chunkHashes (use [] to clear inventory)", http.StatusBadRequest)
		return
	}
	hashes := make([]string, 0, len(report.ChunkHashes))
	seen := make(map[string]bool)
	for _, hash := range report.ChunkHashes {
		if !validLocationHash(hash) {
			http.Error(w, "Chunk hashes must be lowercase SHA-256 hexadecimal strings", http.StatusBadRequest)
			return
		}
		if !seen[hash] {
			hashes = append(hashes, hash)
			seen[hash] = true
		}
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.settings.Coordinator || registry.chunkLocations == nil {
		http.Error(w, "Coordinator chunk-location storage is not connected", http.StatusServiceUnavailable)
		return
	}
	var id string
	err := registry.db.QueryRowContext(r.Context(), "SELECT device_id FROM device_registry WHERE registration_key=?", report.RegistrationKey).Scan(&id)
	if err == sql.ErrNoRows || (err == nil && id != report.DeviceID) {
		http.Error(w, "Register this device before reporting chunks", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, "Cannot verify registered device", http.StatusInternalServerError)
		return
	}
	if err := registry.chunkLocations.ReplaceDeviceChunks(r.Context(), id, hashes); err != nil {
		http.Error(w, "Cannot save chunk inventory", http.StatusInternalServerError)
		return
	}
	writeTransferJSON(w, struct {
		Accepted   bool `json:"accepted"`
		ChunkCount int  `json:"chunkCount"`
	}{true, len(hashes)})
}

//Rabindra Neupane
func (registry *DeviceRegistry) locateChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Only GET requests are allowed", http.StatusMethodNotAllowed)
		return
	}
	hash := r.URL.Query().Get("hash")
	if !validLocationHash(hash) {
		http.Error(w, "Provide a lowercase SHA-256 hash", http.StatusBadRequest)
		return
	}
	registry.mutex.Lock()
	store, coordinator := registry.chunkLocations, registry.settings.Coordinator
	registry.mutex.Unlock()
	if !coordinator || store == nil {
		http.Error(w, "Coordinator chunk-location storage is not connected", http.StatusServiceUnavailable)
		return
	}
	ids, err := store.DeviceIDsForChunk(r.Context(), hash)
	if err != nil {
		http.Error(w, "Cannot look up chunk locations", http.StatusInternalServerError)
		return
	}
	devices, err := registry.readRegistry(r.Context())
	if err != nil {
		http.Error(w, "Cannot read registered devices", http.StatusServiceUnavailable)
		return
	}
	holders := make(map[string]bool, len(ids))
	for _, id := range ids {
		holders[id] = true
	}
	result := ChunkLocations{ChunkHash: hash, Devices: make([]Device, 0)}
	for _, device := range devices {
		if device.DeviceOnline && holders[device.DeviceID] {
			result.Devices = append(result.Devices, device)
		}
	}
	writeTransferJSON(w, result)
}
