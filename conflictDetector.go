// Author: Santosh Tripathee

package main

// The four possible results when a file arrives from another device
type ConflictDecision string

const (
	InSync       ConflictDecision = "in-sync"       // both devices have the same content
	AcceptRemote ConflictDecision = "accept-remote" // only the other device changed it
	KeepLocal    ConflictDecision = "keep-local"    // only this device changed it
	Conflict     ConflictDecision = "conflict"      // both changed it differently
)

// Decides what to do when a file arrives from another device, by comparing
// three content hashes:
//   - baseHash: the version both devices last agreed on ("" if never synced)
//   - localHash: what this device has now ("" if the file doesn't exist here)
//   - remoteHash: what just arrived from the other device
//
// A conflict means both devices edited the file independently, starting
// from the same base, and ended up with different content.
func DetectConflict(baseHash string, localHash string, remoteHash string) ConflictDecision {

	// Same content on both devices, so there's nothing to do,
	// even if both made an edit (they made the same one)
	if localHash == remoteHash {
		return InSync
	}

	// This device doesn't have the file yet, so take the incoming one
	if localHash == "" {
		return AcceptRemote
	}

	// Both devices have the file but never synced it, and the contents differ.
	// They were created separately, so we can't tell which one is right.
	if baseHash == "" {
		return Conflict
	}

	// This device still has the base version, so only the other device edited
	if localHash == baseHash {
		return AcceptRemote
	}

	// The other device still has the base version, so only this device edited
	if remoteHash == baseHash {
		return KeepLocal
	}

	// Both devices changed the file from the same base, differently
	return Conflict
}
