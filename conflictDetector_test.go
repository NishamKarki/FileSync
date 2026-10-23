// Author: Santosh Tripathee

package main

import "testing"

// Checks every conflict rule with a simple example.
// The hashes are short labels like "hashA" instead of real SHA-256 values,
// because DetectConflict only checks whether hashes are equal.
func TestDetectConflict(t *testing.T) {

	testCases := []struct {
		name       string
		baseHash   string
		localHash  string
		remoteHash string
		expected   ConflictDecision
	}{
		{"both devices still have the base version", "hashA", "hashA", "hashA", InSync},
		{"both devices made the same edit", "hashA", "hashB", "hashB", InSync},
		{"only the other device edited", "hashA", "hashA", "hashB", AcceptRemote},
		{"only this device edited", "hashA", "hashB", "hashA", KeepLocal},
		{"both devices edited differently", "hashA", "hashB", "hashC", Conflict},
		{"file is new on this device", "", "", "hashA", AcceptRemote},
		{"created separately with different content", "", "hashA", "hashB", Conflict},
		{"created separately with the same content", "", "hashA", "hashA", InSync},
	}

	for _, testCase := range testCases {
		// t.Run gives each case its own PASS/FAIL line in the output
		t.Run(testCase.name, func(t *testing.T) {
			result := DetectConflict(testCase.baseHash, testCase.localHash, testCase.remoteHash)
			if result != testCase.expected {
				t.Errorf("got %s, expected %s", result, testCase.expected)
			}
		})
	}
}
