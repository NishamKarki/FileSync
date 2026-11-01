// Author: Rabindra Neupane
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
	// Close the completion signal when the heartbeat loop stops.
	defer close(registry.doneSignal)

	// Send a heartbeat every five seconds.
	heartbeatTimer := time.NewTicker(5 * time.Second)
	defer heartbeatTimer.Stop()

	// Continue until the application cancels the registry service.
	for {
		// Send immediately, then wait for a timer, settings change, or shutdown.
		registry.sendHeartbeat()

		// Wait for shutdown, the next heartbeat, or a settings change.
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
	// Read a consistent settings snapshot without holding the lock during network work.
	registry.mutex.Lock()
	deviceSettings := registry.settings
	settingsGeneration := registry.generation
	registry.mutex.Unlock()

	// Stop when a client has no coordinator configured yet.
	if !deviceSettings.Coordinator && deviceSettings.CoordinatorAddress == "" {
		return
	}

	// Limit registration work so an offline coordinator cannot block the service.
	requestContext, cancelRequest := context.WithTimeout(registry.serviceContext, 5*time.Second)
	defer cancelRequest()

	localIP, registrationError := GetLocalIP()
	var registeredDevice Device

	// Continue registration only when the local network address is available.
	if registrationError == nil {
		// Build the identity message sent to the coordinator.
		registration := registrationRequest{
			RegistrationKey: deviceSettings.RegistrationKey,
			DeviceID:        deviceSettings.DeviceID,
			Name:            registry.name,
			Port:            registry.port,
		}

		// Register locally in coordinator mode or contact the remote coordinator.
		if deviceSettings.Coordinator {
			// Register the coordinator directly in its local database.
			registeredDevice, registrationError = registry.acceptRegistration(
				requestContext,
				registration,
				localIP,
				false,
			)
		} else {
			// Choose registration for a new client and heartbeat for a known client.
			requestPath := "/devices/register"
			// Use heartbeat after this installation has an assigned ID.
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

			// Retry full registration when a heartbeat identity is no longer known.
			if registrationError != nil && requestPath == "/devices/heartbeat" {
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

	// Lock before applying the response to shared registry state.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	// Ignore an old response if the user changed the settings meanwhile.
	if settingsGeneration != registry.generation {
		return
	}

	// Save the public local address even when registration fails.
	registry.localAddress = net.JoinHostPort(localIP, strconv.Itoa(registry.port))
	// Validate that successful registration returned a stable device ID.
	if registrationError == nil && registeredDevice.DeviceID == "" {
		registrationError = fmt.Errorf("coordinator returned an empty device ID")
	}

	// Save a newly assigned device ID so it survives application restarts.
	if registrationError == nil && registeredDevice.DeviceID != registry.settings.DeviceID {
		// Copy the current settings before changing the persistent ID.
		updatedSettings := registry.settings
		updatedSettings.DeviceID = registeredDevice.DeviceID

		// Save the new identity before updating shared memory.
		settingsSavingError := registry.saveSettingsLocked(updatedSettings)
		// Keep the old identity when saving fails.
		if settingsSavingError != nil {
			registrationError = settingsSavingError
		} else {
			registry.settings = updatedSettings
		}
	}

	// Update the public registration state after this heartbeat attempt.
	registry.registered = registrationError == nil
	registry.lastError = ""
	// Save and log the latest registration error for the frontend.
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
	// Use GET for reads and POST when request data is present.
	requestMethod := http.MethodGet
	var requestBody io.Reader

	// Encode request data and switch to POST when a body is present.
	if requestData != nil {
		encodedRequest, requestEncodingError := json.Marshal(requestData)
		if requestEncodingError != nil {
			return requestEncodingError
		}
		requestBody = bytes.NewReader(encodedRequest)
		requestMethod = http.MethodPost
	}

	// Create the coordinator request with the caller's timeout context.
	httpRequest, coordinatorRequestCreationError := http.NewRequestWithContext(
		requestContext,
		requestMethod,
		"http://"+coordinatorAddress+requestPath,
		requestBody,
	)
	if coordinatorRequestCreationError != nil {
		return coordinatorRequestCreationError
	}
	// Mark the request body as JSON for coordinator endpoints.
	httpRequest.Header.Set("Content-Type", "application/json")

	// Create a direct client with redirects disabled.
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer httpClient.CloseIdleConnections()

	// Send the request to the selected coordinator.
	httpResponse, coordinatorRequestError := httpClient.Do(httpRequest)
	if coordinatorRequestError != nil {
		return fmt.Errorf("coordinator unavailable: %w", coordinatorRequestError)
	}
	defer httpResponse.Body.Close()

	// Return a bounded coordinator error when the request is rejected.
	if httpResponse.StatusCode != http.StatusOK {
		responseDetails, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return fmt.Errorf(
			"coordinator returned %s: %s",
			httpResponse.Status,
			strings.TrimSpace(string(responseDetails)),
		)
	}

	// Read the successful response with a one-megabyte safety limit.
	responseData, coordinatorResponseReadingError := io.ReadAll(io.LimitReader(httpResponse.Body, (1<<20)+1))
	if coordinatorResponseReadingError != nil {
		return coordinatorResponseReadingError
	}
	// Reject a coordinator response that exceeds the allowed size.
	if len(responseData) > 1<<20 {
		return fmt.Errorf("coordinator response is too large")
	}

	// Decode the coordinator response into the caller's result.
	return json.Unmarshal(responseData, result)
}
