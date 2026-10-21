// /Rabindra Neupane
package network

// FileMetadata describes one proposed file version. Version numbers use the
// existing file_versions convention; BaseVersion is nil for an initial version.
// ChunkHashes are ordered by the same zero-based indexes used by ChunkFile.
//Rabindra Neupane
// Version counters are local; BaseChunkHashes identifies the shared content base.
//Rabindra Neupane
type FileMetadata struct {
	FileID      string `json:"fileId"`
	FileName    string `json:"fileName"`
	Version     int    `json:"version"`
	BaseVersion *int   `json:"baseVersion"`
	//Rabindra Neupane
	// Nil means the base is unknown; an empty list identifies an empty base file.
	BaseChunkHashes []string `json:"baseChunkHashes"`
	//Rabindra Neupane
	// Content identities along the sender's parent chain allow lagging peers to catch up.
	BaseRevisions []string `json:"baseRevisions,omitempty"`
	FileSize      int64    `json:"fileSize"`
	ChunkSize     int64    `json:"chunkSize"`
	ChunkHashes   []string `json:"chunkHashes"`
	FileHash      string   `json:"fileHash"`
}

// FileMetadataComparison identifies chunks whose stored bytes cannot be reused.
// An empty list does not confirm reconstruction or version/conflict acceptance.
//Rabindra Neupane
type FileMetadataComparison struct {
	NeededChunks []int `json:"neededChunks"`
}

//Rabindra Neupane
// TransferPlan identifies an isolated incoming transfer and its missing chunks.
type TransferPlan struct {
	TransferID   string `json:"transferId"`
	NeededChunks []int  `json:"neededChunks"`
}

//Rabindra Neupane
type TransferCompletion struct {
	TransferID string `json:"transferId"`
	Completed  bool   `json:"completed"`
	//Rabindra Neupane
	ConflictFile string `json:"conflictFile,omitempty"`
}

//Rabindra Neupane
// SyncConflictError means the original was kept and the incoming bytes were saved.
type SyncConflictError struct{ ConflictFile string }

//Rabindra Neupane
func (e *SyncConflictError) Error() string {
	return "sync conflict: incoming copy preserved at " + e.ConflictFile
}

type Device struct {
	DeviceID     string `json:"id"`
	DeviceName   string `json:"name"`
	DeviceIP     string `json:"ip"`
	DevicePort   int    `json:"port"`
	DeviceOnline bool   `json:"online"`
}
