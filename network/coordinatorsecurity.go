// Author: Rabindra Neupane
package network

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	deviceIDHeader  = "X-FileSync-Device-ID"
	timestampHeader = "X-FileSync-Timestamp"
	nonceHeader     = "X-FileSync-Nonce"
	signatureHeader = "X-FileSync-Signature"
	requestTimeSkew = 30 * time.Second
	nonceLifetime   = 2 * time.Minute
	pairingLifetime = 2 * time.Minute
)

// Rabindra Neupane
// PairingStatus describes whether the coordinator is accepting one new device.
type PairingStatus struct {
	Active    bool   `json:"active"`
	Code      string `json:"code,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// Rabindra Neupane
// pairingAttempt tracks failed codes from one network address.
type pairingAttempt struct {
	Failures     int
	WindowStart  time.Time
	BlockedUntil time.Time
}

// Rabindra Neupane
// readAuthenticationBody reads a bounded body and restores it for normal JSON decoding.
func readAuthenticationBody(responseWriter http.ResponseWriter, request *http.Request) ([]byte, error) {
	// Limit authentication requests before reading them into memory.
	request.Body = http.MaxBytesReader(responseWriter, request.Body, 8<<20)
	body, bodyReadingError := io.ReadAll(request.Body)
	if bodyReadingError != nil {
		return nil, bodyReadingError
	}
	// Restore the same bytes so the endpoint can decode the request normally.
	request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// Rabindra Neupane
// newPairingCode creates a random six-digit code for a short pairing window.
func newPairingCode() (string, error) {
	// Read random bytes until a value can be converted without modulo bias.
	var randomValue [4]byte
	for {
		if _, randomReadingError := rand.Read(randomValue[:]); randomReadingError != nil {
			return "", randomReadingError
		}
		value := uint32(randomValue[0])<<24 | uint32(randomValue[1])<<16 | uint32(randomValue[2])<<8 | uint32(randomValue[3])
		if value < 4294000000 {
			return fmt.Sprintf("%06d", value%1000000), nil
		}
	}
}

// Rabindra Neupane
// requestSignaturePayload builds the exact data covered by an HMAC signature.
func requestSignaturePayload(method string, requestURI string, timestamp string, nonce string, body []byte) []byte {
	// Hash the body so large JSON requests do not need to be copied into the signature text.
	bodyHash := sha256.Sum256(body)
	return []byte(method + "\n" + requestURI + "\n" + timestamp + "\n" + nonce + "\n" + hex.EncodeToString(bodyHash[:]))
}

// Rabindra Neupane
// signCoordinatorRequest proves that a request came from a device holding its secret.
func signCoordinatorRequest(request *http.Request, body []byte, deviceID string, registrationKey string) error {
	// Decode the saved secret before using it as the HMAC key.
	secret, secretDecodingError := hex.DecodeString(registrationKey)
	if secretDecodingError != nil || len(secret) != 16 {
		return fmt.Errorf("invalid local registration secret")
	}
	// Create a fresh timestamp and nonce for replay protection.
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce, nonceGenerationError := newDeviceToken()
	if nonceGenerationError != nil {
		return nonceGenerationError
	}
	// Sign the request method, path, time, nonce, and body hash.
	signature := hmac.New(sha256.New, secret)
	signature.Write(requestSignaturePayload(request.Method, request.URL.RequestURI(), timestamp, nonce, body))
	request.Header.Set(deviceIDHeader, deviceID)
	request.Header.Set(timestampHeader, timestamp)
	request.Header.Set(nonceHeader, nonce)
	request.Header.Set(signatureHeader, hex.EncodeToString(signature.Sum(nil)))
	return nil
}

// Rabindra Neupane
// verifyRequestSignature validates the HMAC and rejects stale request timestamps.
func verifyRequestSignature(request *http.Request, body []byte, registrationKey string) error {
	// Read and validate the timestamp supplied by the sender.
	timestampText := request.Header.Get(timestampHeader)
	timestampValue, timestampParsingError := strconv.ParseInt(timestampText, 10, 64)
	if timestampParsingError != nil {
		return fmt.Errorf("missing or invalid authentication timestamp")
	}
	requestTime := time.Unix(timestampValue, 0)
	if time.Since(requestTime) > requestTimeSkew || time.Until(requestTime) > requestTimeSkew {
		return fmt.Errorf("authentication timestamp is outside the allowed window")
	}
	// Validate the random nonce and supplied signature format.
	nonce := request.Header.Get(nonceHeader)
	providedSignature, signatureDecodingError := hex.DecodeString(request.Header.Get(signatureHeader))
	if len(nonce) != 32 || signatureDecodingError != nil || len(providedSignature) != sha256.Size {
		return fmt.Errorf("missing or invalid authentication signature")
	}
	// Decode the device secret and calculate the expected signature.
	secret, secretDecodingError := hex.DecodeString(registrationKey)
	if secretDecodingError != nil || len(secret) != 16 {
		return fmt.Errorf("invalid registered device secret")
	}
	expectedSignature := hmac.New(sha256.New, secret)
	expectedSignature.Write(requestSignaturePayload(request.Method, request.URL.RequestURI(), timestampText, nonce, body))
	// Compare signatures in constant time to avoid leaking partial matches.
	if !hmac.Equal(providedSignature, expectedSignature.Sum(nil)) {
		return fmt.Errorf("request authentication failed")
	}
	return nil
}

// Rabindra Neupane
// rememberRequestNonce rejects a valid signed request when its nonce was already used.
func (registry *DeviceRegistry) rememberRequestNonce(principal string, nonce string) error {
	// Lock the replay cache while pruning and adding entries.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	now := time.Now()
	for nonceKey, expiry := range registry.usedNonces {
		if !expiry.After(now) {
			delete(registry.usedNonces, nonceKey)
		}
	}
	// Reject an exact device-and-nonce pair that is still active.
	nonceKey := principal + ":" + nonce
	if _, alreadyUsed := registry.usedNonces[nonceKey]; alreadyUsed {
		return fmt.Errorf("authentication nonce was already used")
	}
	registry.usedNonces[nonceKey] = now.Add(nonceLifetime)
	return nil
}

// Rabindra Neupane
// authenticateRegisteredRequest verifies a signed request from an existing device.
func (registry *DeviceRegistry) authenticateRegisteredRequest(request *http.Request, body []byte, expectedDeviceID string) (string, error) {
	// Require the authenticated header identity to match body data when supplied.
	deviceID := request.Header.Get(deviceIDHeader)
	if deviceID == "" || (expectedDeviceID != "" && deviceID != expectedDeviceID) {
		return "", fmt.Errorf("registered device identity does not match the request")
	}
	// Find the secret assigned to this registered device.
	var registrationKey string
	registrationLookupError := registry.database.QueryRowContext(request.Context(), "SELECT registration_key FROM device_registry WHERE device_id=?", deviceID).Scan(&registrationKey)
	if registrationLookupError == sql.ErrNoRows {
		return "", &coordinatorError{Code: coordinatorErrorDeviceRevoked, Message: "device registration was removed; pair again", StatusCode: http.StatusUnauthorized}
	}
	if registrationLookupError != nil {
		return "", &coordinatorError{Code: coordinatorErrorUnavailable, Message: "coordinator could not read device registration", StatusCode: http.StatusServiceUnavailable}
	}
	// Verify the signature before recording the nonce.
	if signatureVerificationError := verifyRequestSignature(request, body, registrationKey); signatureVerificationError != nil {
		return "", &coordinatorError{Code: coordinatorErrorAuthentication, Message: signatureVerificationError.Error(), StatusCode: http.StatusUnauthorized}
	}
	if nonceRecordingError := registry.rememberRequestNonce(deviceID, request.Header.Get(nonceHeader)); nonceRecordingError != nil {
		return "", &coordinatorError{Code: coordinatorErrorAuthentication, Message: nonceRecordingError.Error(), StatusCode: http.StatusUnauthorized}
	}
	return deviceID, nil
}

// Rabindra Neupane
// pairingCodeMatches compares a submitted pairing code without timing shortcuts.
func pairingCodeMatches(submittedCode string, activeCode string) bool {
	// Require six digits before comparing both values in constant time.
	if len(submittedCode) != 6 || len(activeCode) != 6 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(submittedCode), []byte(activeCode)) == 1
}

// Rabindra Neupane
// pairingAllowedLocked rejects an address that exceeded the pairing attempt limit.
func (registry *DeviceRegistry) pairingAllowedLocked(deviceIP string) bool {
	// Remove an expired attempt record before checking the current address.
	attempt, exists := registry.pairingAttempts[deviceIP]
	if !exists {
		return true
	}
	now := time.Now()
	if attempt.BlockedUntil.After(now) {
		return false
	}
	if now.Sub(attempt.WindowStart) > pairingLifetime {
		delete(registry.pairingAttempts, deviceIP)
	}
	return true
}

// Rabindra Neupane
// recordPairingFailureLocked counts bad codes and temporarily blocks repeated guesses.
func (registry *DeviceRegistry) recordPairingFailureLocked(deviceIP string) {
	// Start a new failure window when no recent attempts exist.
	now := time.Now()
	attempt := registry.pairingAttempts[deviceIP]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) > pairingLifetime {
		attempt = pairingAttempt{WindowStart: now}
	}
	attempt.Failures++
	// Block this address after five incorrect codes.
	if attempt.Failures >= 5 {
		attempt.BlockedUntil = now.Add(5 * time.Minute)
	}
	registry.pairingAttempts[deviceIP] = attempt
}

// Rabindra Neupane
// StartPairing opens a short single-use registration window on the coordinator.
func (registry *DeviceRegistry) StartPairing() (PairingStatus, error) {
	// Create the code before locking shared coordinator state.
	pairingCode, pairingCodeGenerationError := newPairingCode()
	if pairingCodeGenerationError != nil {
		return PairingStatus{}, pairingCodeGenerationError
	}
	// Save the temporary code only when this device is the coordinator.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.settings.Coordinator {
		return PairingStatus{}, fmt.Errorf("only the coordinator can allow a new device")
	}
	registry.activePairingCode = pairingCode
	registry.activePairingUntil = time.Now().Add(pairingLifetime)
	return PairingStatus{Active: true, Code: pairingCode, ExpiresAt: registry.activePairingUntil.UTC().Format(time.RFC3339)}, nil
}

// Rabindra Neupane
// GetPairingStatus returns the current local coordinator pairing window.
func (registry *DeviceRegistry) GetPairingStatus() PairingStatus {
	// Lock pairing state while checking whether the code has expired.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.activePairingCode == "" || !registry.activePairingUntil.After(time.Now()) {
		registry.activePairingCode = ""
		registry.activePairingUntil = time.Time{}
		return PairingStatus{}
	}
	return PairingStatus{Active: true, Code: registry.activePairingCode, ExpiresAt: registry.activePairingUntil.UTC().Format(time.RFC3339)}
}

// Rabindra Neupane
// RevokeDevice removes a registered client so its secret can no longer authenticate.
func (registry *DeviceRegistry) RevokeDevice(deviceID string) error {
	// Lock coordinator state while validating and deleting the registration.
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.settings.Coordinator {
		return fmt.Errorf("only the coordinator can remove registered devices")
	}
	if deviceID == "" || deviceID == registry.settings.DeviceID {
		return fmt.Errorf("select a client device to remove")
	}
	// Delete the credential so future signed requests are rejected immediately.
	deletionResult, deviceDeletionError := registry.database.Exec("DELETE FROM device_registry WHERE device_id=?", deviceID)
	if deviceDeletionError != nil {
		return deviceDeletionError
	}
	deletedRows, deletedRowReadingError := deletionResult.RowsAffected()
	if deletedRowReadingError != nil {
		return deletedRowReadingError
	}
	if deletedRows == 0 {
		return fmt.Errorf("registered device was not found")
	}
	return nil
}
