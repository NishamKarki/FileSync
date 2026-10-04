// /Rabindra Neupane
package network

type Device struct {
	DeviceID     string `json:"id"`
	DeviceName   string `json:"name"`
	DeviceIP     string `json:"ip"`
	DevicePort   int    `json:"port"`
	DeviceOnline bool   `json:"online"`
}
