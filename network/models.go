// /Rabindra Neupane
package network

// FileMetadata describes one proposed file version. Version numbers use the
// existing file_versions convention; BaseVersion is nil for an initial version.
// ChunkHashes are ordered by the same zero-based indexes used by ChunkFile.
//Rabindra Neupane
type FileMetadata struct {
	FileID      string   `json:"fileId"`
	FileName    string   `json:"fileName"`
	Version     int      `json:"version"`
	BaseVersion *int     `json:"baseVersion"`
	FileSize    int64    `json:"fileSize"`
	ChunkSize   int64    `json:"chunkSize"`
	ChunkHashes []string `json:"chunkHashes"`
	FileHash    string   `json:"fileHash"`
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
}

type Device struct {
	DeviceID     string `json:"id"`
	DeviceName   string `json:"name"`
	DeviceIP     string `json:"ip"`
	DevicePort   int    `json:"port"`
	DeviceOnline bool   `json:"online"`
}
