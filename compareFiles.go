package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// This function is run when file with same names have distinct data
// When AddFile function from filePicker.go detects a files already exists
// it checks the content to determine whether it is the same file or a different one
func CompareFiles(firstFilePath string, secondFilePath string) bool {

	firstFile, firstFileOpenError := os.Open(firstFilePath)
	if firstFileOpenError != nil {
		fmt.Println(firstFileOpenError)
		return false
	}
	defer firstFile.Close()

	secondFile, secondFileOpenError := os.Open(secondFilePath)
	if secondFileOpenError != nil {
		fmt.Println(secondFileOpenError)
		return false
	}

	defer secondFile.Close()

	// Create SHA 256 hashers for each files
	firstFileHasher := sha256.New()
	secondFileHasher := sha256.New()

	io.Copy(firstFileHasher, firstFile)
	io.Copy(secondFileHasher, secondFile)

	firstFileHash := firstFileHasher.Sum(nil)
	secondFileHash := secondFileHasher.Sum(nil)

	return bytes.Equal(firstFileHash, secondFileHash)
}
