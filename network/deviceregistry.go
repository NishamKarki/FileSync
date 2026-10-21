/// Rabindra Neupane

package network

import (
	"sync"
	"time"
)

// RegisteredDevice represents a device that has been registered with the network.
type RegisteredDevice struct {
	Name     string    `json:"name"`
	IP       string    `json:"ip"`
	Port     string    `json:"port"`
	LastSeen time.Time `json:"last_seen"`
	Online   bool      `json:"online"`
}

var (
	deviceRegistry = make(map[string]RegisteredDevice)
	registryMutex  sync.RWMutex
)

// RegisterDevice adds a device to the registry with the given name, IP, and port.
func RegisterDevice(name string, ip string, port string) {
	registryMutex.Lock()
	defer registryMutex.Unlock()

	deviceRegistry[ip] = RegisteredDevice{
		Name:     name,
		IP:       ip,
		Port:     port,
		LastSeen: time.Now(),
		Online:   true,
	}
}

// GetRegisteredDevice retrieves all registered devices from the registry.
func GetRegisteredDevices() []RegisteredDevice {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	devices := make([]RegisteredDevice, 0, len(deviceRegistry))
	for _, device := range deviceRegistry {
		devices = append(devices, device)
	}
	return devices
}

// MarkDeviceOffline marks a device as offline based on its IP address.
func MarkDeviceOffline(ip string) {
	registryMutex.Lock()
	defer registryMutex.Unlock()

	device, exists := deviceRegistry[ip]
	if !exists {
		return
	}
	device.Online = false
	deviceRegistry[ip] = device
}

func MarkAllDevicesOffline() {
	registryMutex.Lock()
	defer registryMutex.Unlock()

	for ip, device := range deviceRegistry {
		device.Online = false
		deviceRegistry[ip] = device
	}
}
