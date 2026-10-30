package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Rabindra Neupane
// run keeps this device registered while FileSync is running.
func (registry *DeviceRegistry) run() {
	defer close(registry.doneSignal)

	// Send a heartbeat every five seconds.
	heartbeatTimer := time.NewTicker(5 * time.Second)
	defer heartbeatTimer.Stop()

	for {
		registry.sendHeartbeat()

		select {
		case <-registry.serviceContext.Done():
			return
		case <-heartbeatTimer.C:
		case <-registry.wakeSignal:
		}
	}
}

// Rabindra Neupane
// sendHeartbeat registers this device or updates its online status.
func (registry *DeviceRegistry) sendHeartbeat() {
	registry.mutex.Lock()
	deviceSettings := registry.settings
	settingsGeneration := registry.generation
	registry.mutex.Unlock()

	if !deviceSettings.Coordinator && deviceSettings.CoordinatorAddress == "" {
		return
	}

	requestContext, cancelRequest := context.WithTimeout(registry.serviceContext, 5*time.Second)
	defer cancelRequest()

	localIP, registrationError := GetLocalIP()
	var registeredDevice Device

	if registrationError == nil {
		registration := registrationRequest{
			RegistrationKey: deviceSettings.RegistrationKey,
			DeviceID:        deviceSettings.DeviceID,
			Name:            registry.name,
			Port:            registry.port,
		}

		if deviceSettings.Coordinator {
			registeredDevice, registrationError = registry.acceptRegistration(
				requestContext,
				registration,
				localIP,
				false,
			)
		} else {
			requestPath := "/devices/register"
			if registration.DeviceID != "" {
				requestPath = "/devices/heartbeat"
			}

			registrationError = registry.registryRequest(
				requestContext,
				deviceSettings.CoordinatorAddress,
				requestPath,
				registration,
				&registeredDevice,
			)

			if registrationError != nil && requestPath == "/devices/heartbeat" {
				// Register again if the coordinator no longer knows this device.
				registrationError = registry.registryRequest(
					requestContext,
					deviceSettings.CoordinatorAddress,
					"/devices/register",
					registration,
					&registeredDevice,
				)
			}
		}
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	// Ignore an old response if the user changed the settings meanwhile.
	if settingsGeneration != registry.generation {
		return
	}

	registry.localAddress = net.JoinHostPort(localIP, strconv.Itoa(registry.port))
	if registrationError == nil && registeredDevice.DeviceID == "" {
		registrationError = fmt.Errorf("coordinator returned an empty device ID")
	}

	if registrationError == nil && registeredDevice.DeviceID != registry.settings.DeviceID {
		updatedSettings := registry.settings
		updatedSettings.DeviceID = registeredDevice.DeviceID

		settingsSavingError := registry.saveSettingsLocked(updatedSettings)
		if settingsSavingError != nil {
			registrationError = settingsSavingError
		} else {
			registry.settings = updatedSettings
		}
	}

	registry.registered = registrationError == nil
	registry.lastError = ""
	if registrationError != nil {
		registry.lastError = registrationError.Error()
		fmt.Println("Device registration:", registrationError)
	}
}

// Rabindra Neupane
// registryRequest sends a request to the selected coordinator.
func (registry *DeviceRegistry) registryRequest(
	requestContext context.Context,
	coordinatorAddress string,
	requestPath string,
	requestData interface{},
	result interface{},
) error {
	requestMethod := http.MethodGet
	var requestBody io.Reader

	if requestData != nil {
		encodedRequest, requestEncodingError := json.Marshal(requestData)
		if requestEncodingError != nil {
			return requestEncodingError
		}
		requestBody = bytes.NewReader(encodedRequest)
		requestMethod = http.MethodPost
	}

	httpRequest, coordinatorRequestCreationError := http.NewRequestWithContext(
		requestContext,
		requestMethod,
		"http://"+coordinatorAddress+requestPath,
		requestBody,
	)
	if coordinatorRequestCreationError != nil {
		return coordinatorRequestCreationError
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer httpClient.CloseIdleConnections()

	httpResponse, coordinatorRequestError := httpClient.Do(httpRequest)
	if coordinatorRequestError != nil {
		return fmt.Errorf("coordinator unavailable: %w", coordinatorRequestError)
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode != http.StatusOK {
		responseDetails, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return fmt.Errorf(
			"coordinator returned %s: %s",
			httpResponse.Status,
			strings.TrimSpace(string(responseDetails)),
		)
	}

	responseData, coordinatorResponseReadingError := io.ReadAll(io.LimitReader(httpResponse.Body, (1<<20)+1))
	if coordinatorResponseReadingError != nil {
		return coordinatorResponseReadingError
	}
	if len(responseData) > 1<<20 {
		return fmt.Errorf("coordinator response is too large")
	}

	return json.Unmarshal(responseData, result)
}
