// Author: Rabindra Neupane
package network

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
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
	pendingPairingCode := registry.pendingPairingCode
	pairingRetryPaused := registry.pairingRetryPaused
	nextAttempt := registry.nextAttempt
	settingsGeneration := registry.generation
	registry.mutex.Unlock()

	// Stop when a client has no coordinator configured yet.
	if !deviceSettings.Coordinator && deviceSettings.CoordinatorAddress == "" {
		return
	}
	// Pause rejected pairing attempts until the user submits a new code.
	if !deviceSettings.Coordinator && pairingRetryPaused {
		return
	}
	// Wait for the current backoff period before another network attempt.
	if nextAttempt.After(time.Now()) {
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
			PairingCode:     pendingPairingCode,
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
			// Choose registration for a new client or an explicit re-pairing attempt.
			requestPath := "/devices/register"
			// Use heartbeat after this installation has an assigned ID.
			if registration.DeviceID != "" && pendingPairingCode == "" {
				requestPath = "/devices/heartbeat"
				registration.RegistrationKey = ""
				registration.PairingCode = ""
			}

			registrationError = registry.registryRequest(
				requestContext,
				deviceSettings.CoordinatorAddress,
				requestPath,
				registration,
				&registeredDevice,
			)

			// Retry full registration when a heartbeat identity is no longer known.
			if registrationError != nil && requestPath == "/devices/heartbeat" && pendingPairingCode != "" {
				// Restore the secret field required by the registration endpoint.
				registration.RegistrationKey = deviceSettings.RegistrationKey
				registration.PairingCode = pendingPairingCode
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
	// Require a stable coordinator identity on every successful remote response.
	if registrationError == nil && !deviceSettings.Coordinator {
		if registeredDevice.CoordinatorID == "" {
			registrationError = &coordinatorError{Code: coordinatorErrorIdentityChanged, Message: "coordinator did not provide a trusted identity", StatusCode: http.StatusUnauthorized}
		} else if deviceSettings.CoordinatorID != "" && registeredDevice.CoordinatorID != deviceSettings.CoordinatorID && pendingPairingCode == "" {
			registrationError = &coordinatorError{Code: coordinatorErrorIdentityChanged, Message: "coordinator identity changed; enter a new pairing code to trust it", StatusCode: http.StatusUnauthorized}
		}
	}

	// Save newly assigned device and coordinator identities across application restarts.
	if registrationError == nil && (registeredDevice.DeviceID != registry.settings.DeviceID ||
		(registeredDevice.CoordinatorID != "" && registeredDevice.CoordinatorID != registry.settings.CoordinatorID)) {
		// Copy the current settings before changing the persistent ID.
		updatedSettings := registry.settings
		updatedSettings.DeviceID = registeredDevice.DeviceID
		if registeredDevice.CoordinatorID != "" {
			updatedSettings.CoordinatorID = registeredDevice.CoordinatorID
		}

		// Save the new identity before updating shared memory.
		settingsSavingError := registry.saveSettingsLocked(updatedSettings)
		// Keep the old identity when saving fails.
		if settingsSavingError != nil {
			registrationError = settingsSavingError
		} else {
			registry.settings = updatedSettings
		}
	}
	// Clear a submitted pairing code after the coordinator accepts registration.
	if registrationError == nil {
		registry.pendingPairingCode = ""
		registry.pairingRetryPaused = false
		registry.registered = true
		registry.registrationState = RegistrationRegistered
		registry.lastError = ""
		registry.retryDelay = 5 * time.Second
		registry.nextAttempt = time.Now().Add(5 * time.Second)
		return
	}

	// Update the public registration state after this heartbeat attempt.
	registry.registered = false
	// Save and log the latest registration error for the frontend.
	if registrationError != nil {
		// Stop authentication failures until the user supplies a new pairing code.
		errorCode := coordinatorErrorCode(registrationError)
		switch errorCode {
		case coordinatorErrorPairingRequired, coordinatorErrorPairingInvalid, coordinatorErrorPairingBlocked:
			registry.pendingPairingCode = ""
			registry.pairingRetryPaused = true
			registry.registrationState = RegistrationPairingNeeded
			registry.nextAttempt = time.Time{}
		case coordinatorErrorDeviceRevoked:
			registry.pendingPairingCode = ""
			registry.pairingRetryPaused = true
			registry.registrationState = RegistrationRevoked
			registry.nextAttempt = time.Time{}
		case coordinatorErrorIdentityChanged:
			registry.pendingPairingCode = ""
			registry.pairingRetryPaused = true
			registry.registrationState = RegistrationPairingNeeded
			registry.nextAttempt = time.Time{}
		default:
			// Retry temporary failures with a bounded exponential delay.
			if deviceSettings.DeviceID != "" {
				registry.registrationState = RegistrationReconnecting
			} else {
				registry.registrationState = RegistrationRegistering
			}
			registry.scheduleRetryLocked()
		}
		registry.lastError = registrationError.Error()
		fmt.Println("Device registration:", registrationError)
	}
}

// Rabindra Neupane
// coordinatorErrorCode returns a stable coordinator recovery code when available.
func coordinatorErrorCode(requestError error) string {
	// Read typed coordinator failures without depending on their displayed message.
	var classifiedError *coordinatorError
	if errors.As(requestError, &classifiedError) {
		return classifiedError.Code
	}
	return ""
}

// Rabindra Neupane
// scheduleRetryLocked increases temporary retry delays up to thirty seconds.
func (registry *DeviceRegistry) scheduleRetryLocked() {
	// Start at five seconds and add small jitter so clients do not retry together.
	if registry.retryDelay < 5*time.Second {
		registry.retryDelay = 5 * time.Second
	}
	retryJitter := time.Duration(0)
	if randomDelay, randomDelayError := cryptorand.Int(cryptorand.Reader, big.NewInt(2001)); randomDelayError == nil {
		retryJitter = time.Duration(randomDelay.Int64()) * time.Millisecond
	}
	registry.nextAttempt = time.Now().Add(registry.retryDelay + retryJitter)
	registry.retryDelay *= 2
	if registry.retryDelay > 30*time.Second {
		registry.retryDelay = 30 * time.Second
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
	var encodedRequest []byte

	// Encode request data and switch to POST when a body is present.
	if requestData != nil {
		var requestEncodingError error
		encodedRequest, requestEncodingError = json.Marshal(requestData)
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
	// Read the persistent identity without holding the lock during network work.
	registry.mutex.Lock()
	deviceID := registry.settings.DeviceID
	registrationKey := registry.settings.RegistrationKey
	registry.mutex.Unlock()
	// Sign every coordinator request with this installation's private secret.
	if requestSigningError := signCoordinatorRequest(httpRequest, encodedRequest, deviceID, registrationKey); requestSigningError != nil {
		return requestSigningError
	}

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
		var classifiedError coordinatorError
		if json.Unmarshal(responseDetails, &classifiedError) == nil && classifiedError.Code != "" {
			classifiedError.StatusCode = httpResponse.StatusCode
			return &classifiedError
		}
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
