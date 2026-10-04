// Author: Nisham Karki

package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// This function is run when file with same names have distinct data
// It is used by AddFile when it detects that a file with the same name already
// exists inside the Synced Files Folder
// It checks the content to determine whether it is the same file or a different one
// SHA-256 is generated for two file and compared
// Returns True if both files contains the same data, otherwise False
func CompareFiles(firstFilePath string, secondFilePath string) bool {

	// Open the first file to read
	firstFile, firstFileOpenError := os.Open(firstFilePath)

	// Check error during opening the first file
	if firstFileOpenError != nil {
		fmt.Println(firstFileOpenError)
		return false
	}

	// Closes the first file when this function is finished
	defer firstFile.Close()

	// Open the second file to read
	secondFile, secondFileOpenError := os.Open(secondFilePath)

	// Check error during opening the second file
	if secondFileOpenError != nil {
		fmt.Println(secondFileOpenError)
		return false
	}

	// Closes the second file when this function is finished
	defer secondFile.Close()

	// Create SHA 256 hashers for each files
	firstFileHasher := sha256.New()
	secondFileHasher := sha256.New()

	// Read each of the files and send its contents to their related SHA-256 hasher
	io.Copy(firstFileHasher, firstFile)
	io.Copy(secondFileHasher, secondFile)

	// GEt the SHA-256 hash value for each file
	firstFileHash := firstFileHasher.Sum(nil)
	secondFileHash := secondFileHasher.Sum(nil)

	// Compare both hashes and return boolean value
	// If hashes match, it indicates that both files contain the same data
	return bytes.Equal(firstFileHash, secondFileHash)
}
