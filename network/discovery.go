package network

import (
	"fmt"
	"net"
)

// GetLocalIP retrieves the local IP address of the machine
func GetLocalIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	// Check for error during connection
	if err != nil {
		return "", err
	}
	defer conn.Close()
	// Get the local address from the connection
	localAddress := conn.LocalAddr().(*net.UDPAddr)
	// Print the local IP address to the console
	fmt.Println("FileSync local IP address:", localAddress.IP.String())
	return localAddress.IP.String(), nil
}

// DiscoverDevices discovers devices on the local network
func DiscoverDevices() []string {
	// Implementation for device discovery
	localIP, err := GetLocalIP()
	if err != nil {
		fmt.Println("Could not get local IP address:", err)
		return []string{}
	}
	// Parse the local IP address to get the network prefix
	ip := net.ParseIP(localIP).To4()
	if ip == nil {
		fmt.Println("Could not parse local IPv4 address:", localIP)
		return []string{}
	}

	results := make(chan string, 254)

	// Scan the local network for devices
	for i := 1; i <= 254; i++ {
		address := fmt.Sprintf(
			"%d.%d.%d.%d",
			ip[0],
			ip[1],
			ip[2],
			i)

		// Skip the local device itself
		if address == localIP {
			continue
		}

		go func(deviceIP string) {
			// Ping the device at the specified port 8080
			deviceAddress := deviceIP + ":8080"
			response, err := PingDevice(deviceAddress)
			// Check if the ping was successful and the device is online
			if err == nil && response.Success {
				fmt.Println("FileSync device found:", deviceAddress)
				results <- deviceAddress
				return
			}
			// If the ping failed or the device is offline, send an empty string to the results channel
			results <- ""
		}(address)
	}
	// Collect the results from the goroutines
	devices := make([]string, 0)
	// Wait for all goroutines to finish and collect the results
	for i := 0; i < 253; i++ {
		device := <-results
		if device != "" {
			devices = append(devices, device)
		}
	}
	return devices
}
