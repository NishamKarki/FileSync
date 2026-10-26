// Author: Nisham Karki

package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// CalculateFileChecksum generates a SHA-256 checksum for a complete file.
//
// The file is read through a SHA-256 hasher without loading the entire
// file into memory at once. The returned checksum is a hexadecimal string.
func CalculateFileChecksum(filePath string) (string, error) {

	// Open the file that needs to be hashed.
	fileToHash, fileOpenError :=
		os.Open(filePath)

	if fileOpenError != nil {
		return "", fileOpenError
	}

	// Close the file after checksum generation is complete.
	defer fileToHash.Close()

	// Create a SHA-256 hasher.
	fileHasher :=
		sha256.New()

	// Read the file and send its contents into the hasher.
	_, fileHashingError :=
		io.Copy(
			fileHasher,
			fileToHash,
		)

	if fileHashingError != nil {
		return "", fileHashingError
	}

	// Convert the SHA-256 value into hexadecimal format.
	fileChecksum :=
		fmt.Sprintf(
			"%x",
			fileHasher.Sum(nil),
		)

	return fileChecksum, nil
}

// VerifyFileIntegrity compares the complete SHA-256 checksum of an
// original file with the checksum of its reconstructed copy.
//
// Matching hashes mean both files contain the same data.
func VerifyFileIntegrity(
	originalFilePath string,
	reconstructedFilePath string,
) (bool, error) {

	// Generate checksum for the original file.
	originalChecksum, originalChecksumError :=
		CalculateFileChecksum(originalFilePath)

	if originalChecksumError != nil {
		return false, originalChecksumError
	}

	// Generate checksum for the reconstructed file.
	reconstructedChecksum, reconstructedChecksumError :=
		CalculateFileChecksum(reconstructedFilePath)

	if reconstructedChecksumError != nil {
		return false, reconstructedChecksumError
	}

	fmt.Println(
		"Original file checksum:",
		originalChecksum,
	)

	fmt.Println(
		"Reconstructed file checksum:",
		reconstructedChecksum,
	)

	// Both checksum values must match for the reconstruction
	// to be considered correct.
	return originalChecksum == reconstructedChecksum, nil
}
