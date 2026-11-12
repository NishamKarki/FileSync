// Author: Rabindra Neupane
package network

import "context"

// Rabindra Neupane
// FileMetadata describes one proposed file version and its ordered chunk hashes.
// Base fields identify the shared version used for incremental comparison.
// Version counters are local; BaseChunkHashes identifies the shared content base.
type FileMetadata struct {
	// Conflict contract agreed with Santosh Tripathee; base is a whole-file SHA-256.
	BaseHash    string `json:"baseHash"`
	DeviceID    string `json:"deviceId"`
	FileID      string `json:"fileId"`
	FileName    string `json:"fileName"`
	Version     int    `json:"version"`
	BaseVersion *int   `json:"baseVersion"`
	// Nil means the base is unknown; an empty list identifies an empty base file.
	BaseChunkHashes []string `json:"baseChunkHashes"`
	// Content identities along the sender's parent chain allow lagging peers to catch up.
	BaseRevisions []string `json:"baseRevisions,omitempty"`
	FileSize      int64    `json:"fileSize"`
	ChunkSize     int64    `json:"chunkSize"`
	ChunkHashes   []string `json:"chunkHashes"`
	FileHash      string   `json:"fileHash"`
}

// Rabindra Neupane
// TransferPlan identifies an isolated incoming transfer and the chunks it needs.
type TransferPlan struct {
	TransferID   string `json:"transferId"`
	NeededChunks []int  `json:"neededChunks"`
}

// Rabindra Neupane
// TransferCompletion is the receiver's final acknowledgement. KeepLocal and
// ConflictFile explain valid outcomes where the incoming file was not applied.
type TransferCompletion struct {
	KeepLocal    bool   `json:"keepLocal,omitempty"`
	FileHash     string `json:"fileHash,omitempty"`
	TransferID   string `json:"transferId"`
	Completed    bool   `json:"completed"`
	ConflictFile string `json:"conflictFile,omitempty"`
}

// Rabindra Neupane
// SyncConflictError reports that the local file was kept and the incoming copy was saved.
type SyncConflictError struct{ ConflictFile string }

// Rabindra Neupane
// KeepLocalError reports that the receiver already has a newer file version.
type KeepLocalError struct{}

// Rabindra Neupane
// Error describes the recovery action after the receiver keeps its local file.
func (*KeepLocalError) Error() string {
	// Return the recovery message shown to the sender.
	return "receiver has a newer version; pull it before syncing this older copy"
}

// Rabindra Neupane
// Error reports where the receiver preserved the incoming conflict copy.
func (e *SyncConflictError) Error() string {
	// Return the path where the incoming copy was preserved.
	return "sync conflict: incoming copy preserved at " + e.ConflictFile
}

// Rabindra Neupane
// Device is the public coordinator record used by discovery and the frontend.
type Device struct {
	DeviceID     string `json:"id"`
	DeviceName   string `json:"name"`
	DeviceIP     string `json:"ip"`
	DevicePort   int    `json:"port"`
	DeviceOnline bool   `json:"online"`
	LastSeen     string `json:"lastSeen"`
	Coordinator  bool   `json:"coordinator"`
}

// Rabindra Neupane
// ChunkLocationStore defines the database operations needed for chunk locations.
// The persistent implementation belongs to Santosh's metadata layer.
// ReplaceDeviceChunks atomically replaces the entire inventory, including an
// empty inventory. DeviceIDsForChunk returns unique device IDs for that hash.
type ChunkLocationStore interface {
	ReplaceDeviceChunks(context.Context, string, []string) error
	DeviceIDsForChunk(context.Context, string) ([]string, error)
}

// Rabindra Neupane
// ChunkReport carries one registered installation's complete chunk inventory.
type ChunkReport struct {
	DeviceID    string   `json:"deviceId"`
	ChunkHashes []string `json:"chunkHashes"`
}

// Rabindra Neupane
// ChunkLocations lists online registered devices that report a verified chunk.
type ChunkLocations struct {
	ChunkHash string   `json:"chunkHash"`
	Devices   []Device `json:"devices"`
}
