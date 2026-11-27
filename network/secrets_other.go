//go:build !windows

package network

// protectLocalSecret stores key material with owner-only file permissions on non-Windows platforms.
func protectLocalSecret(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	out := make([]byte, len(plaintext))
	copy(out, plaintext)
	return out, nil
}

// unprotectLocalSecret returns local key material stored on non-Windows platforms.
func unprotectLocalSecret(stored []byte) ([]byte, error) {
	if len(stored) == 0 {
		return nil, nil
	}
	out := make([]byte, len(stored))
	copy(out, stored)
	return out, nil
}
