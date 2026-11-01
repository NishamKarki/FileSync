// Author: Rabindra Neupane
package network

import (
	"fmt"
	"net"
)

// Rabindra Neupane
// GetLocalIP finds the local address used to reach devices on the network.
func GetLocalIP() (string, error) {
	// Create a UDP route only to learn which local interface is active.
	connection, networkDialError := net.Dial("udp", "8.8.8.8:80")
	// Check whether an active network route is available.
	if networkDialError != nil {
		return "", networkDialError
	}
	// Close the temporary route after reading its local address.
	defer connection.Close()
	// Read the local IP selected by the operating system.
	localAddress := connection.LocalAddr().(*net.UDPAddr)
	// Return the address without printing on every heartbeat.
	return localAddress.IP.String(), nil
}

// Rabindra Neupane
// DiscoverDevices scans the local IPv4 subnet for FileSync ping responses.
func DiscoverDevices() []Device {
	// Read the active local IP before building the subnet range.
	localIP, localIPLookupError := GetLocalIP()
	if localIPLookupError != nil {
		fmt.Println("Could not get local IP address:", localIPLookupError)
		return []Device{}
	}
	// Validate that the address can be scanned as IPv4.
	localIP4 := net.ParseIP(localIP).To4()
	if localIP4 == nil {
		fmt.Println("Could not parse local IPv4 address:", localIP)
		return []Device{}
	}

	// Create a buffered result channel so every scan can finish independently.
	results := make(chan Device, 254)
	// Create one ping task for every possible address on the local subnet.
	for i := 1; i <= 254; i++ {
		deviceIP := fmt.Sprintf(
			"%d.%d.%d.%d",
			localIP4[0],
			localIP4[1],
			localIP4[2],
			i)

		// Skip this computer because it is already known.
		if deviceIP == localIP {
			continue
		}

		go func(deviceIP string) {
			// Check the standard FileSync port on this address.
			deviceAddress := deviceIP + ":8080"
			response, devicePingError := PingDevice(deviceAddress)
			// Save a complete device record only for a valid FileSync response.
			if devicePingError == nil && response.Success {
				device := Device{
					DeviceID:     response.DeviceID,
					Coordinator:  response.Coordinator,
					DeviceName:   response.DeviceName,
					DeviceIP:     deviceIP,
					DevicePort:   8080,
					DeviceOnline: true,
				}

				fmt.Println("FileSync device found:", device.DeviceName, device.DeviceIP)
				results <- device
				return
			}
			// Respond with an empty record so the collection loop still completes.
			results <- Device{}
		}(deviceIP)
	}
	// Create the final list of discovered devices.
	devices := make([]Device, 0)
	// Read one result for every remote address that was scanned.
	for index := 0; index < 253; index++ {
		// Wait for the next scan result from a remote address.
		device := <-results
		// Keep only addresses that returned a valid FileSync device.
		if device.DeviceIP != "" {
			devices = append(devices, device)
		}
	}

	// Return every FileSync device found on the local subnet.
	return devices
}
