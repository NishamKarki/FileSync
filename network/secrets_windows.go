//go:build windows

package network

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const dpapiPrefix = "DPAPIv1:"

// protectLocalSecret encrypts sensitive key material for the current Windows user with DPAPI.
func protectLocalSecret(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	inBlob := windows.DataBlob{
		Size: uint32(len(plaintext)),
		Data: &plaintext[0],
	}
	var outBlob windows.DataBlob
	if err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &outBlob); err != nil {
		return nil, fmt.Errorf("DPAPI protect failed: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))

	protectedBytes := unsafe.Slice(outBlob.Data, outBlob.Size)
	encoded := base64.StdEncoding.EncodeToString(protectedBytes)
	return []byte(dpapiPrefix + encoded), nil
}

// unprotectLocalSecret decrypts Windows DPAPI-protected key material.
func unprotectLocalSecret(stored []byte) ([]byte, error) {
	if len(stored) == 0 {
		return nil, nil
	}
	if !bytes.HasPrefix(stored, []byte(dpapiPrefix)) {
		// Allow one-time migration of legacy unencrypted local secrets.
		out := make([]byte, len(stored))
		copy(out, stored)
		return out, nil
	}
	encoded := string(stored[len(dpapiPrefix):])
	cipherBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode DPAPI secret: %w", err)
	}
	if len(cipherBytes) == 0 {
		return nil, nil
	}
	inBlob := windows.DataBlob{
		Size: uint32(len(cipherBytes)),
		Data: &cipherBytes[0],
	}
	var outBlob windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &outBlob); err != nil {
		return nil, fmt.Errorf("DPAPI unprotect failed: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))

	decryptedBytes := unsafe.Slice(outBlob.Data, outBlob.Size)
	result := make([]byte, len(decryptedBytes))
	copy(result, decryptedBytes)
	return result, nil
}
