// Santosh Tripathee
// Performance evaluation for FileSync.
// Every outgoing sync is saved as one row in "Performance Logs/sync_metrics.csv",
// so we can compare chunk sync (Sync Now) with whole-file sync in the final report.
package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// SyncMetrics is one row in sync_metrics.csv
type SyncMetrics struct {
	FileName          string
	Mode              string // "incremental" (Sync Now) or "whole-file" (Send Whole File)
	FileSizeBytes     int64
	ChunkSizeBytes    int64
	TotalChunks       int
	ChunksSent        int
	ChunkBytesSent    int64
	PrepareTime       time.Duration // reading, chunking and hashing the file
	TransferTime      time.Duration // sending, until the receiver confirms
	CPUTime           time.Duration // CPU used by FileSync during this sync
	ChunkStorageBytes int64         // size of the chunk folder after the sync
	Outcome           string        // synced, conflict, keep-local or failed
}

// Only one goroutine writes to a log file at a time
var performanceLogMutex sync.Mutex

var syncMetricsHeader = []string{
	"time", "file", "mode", "file_size_bytes", "chunk_size_bytes",
	"total_chunks", "chunks_sent", "chunk_bytes_sent", "bytes_saved_percent",
	"prepare_ms", "transfer_ms", "total_ms", "cpu_ms", "chunk_storage_bytes", "outcome",
}

// The log folder sits next to "Synced Files"
func (app *App) performanceLogFolder() string {
	return filepath.Join(filepath.Dir(app.syncFolderPath), "Performance Logs")
}

// Adds one row to a CSV file. Writes the header first if the file is new.
func appendCSVRow(logFilePath string, header []string, row []string) error {
	performanceLogMutex.Lock()
	defer performanceLogMutex.Unlock()

	if folderCreationError := os.MkdirAll(filepath.Dir(logFilePath), 0755); folderCreationError != nil {
		return folderCreationError
	}
	_, fileStatError := os.Stat(logFilePath)
	isNewLogFile := os.IsNotExist(fileStatError)

	logFile, fileOpenError := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if fileOpenError != nil {
		return fileOpenError
	}
	defer logFile.Close()

	csvWriter := csv.NewWriter(logFile)
	if isNewLogFile {
		csvWriter.Write(header)
	}
	csvWriter.Write(row)
	csvWriter.Flush()
	return csvWriter.Error()
}

// Milliseconds with 3 decimals, e.g. 12.345
func formatMilliseconds(duration time.Duration) string {
	return strconv.FormatFloat(float64(duration.Microseconds())/1000, 'f', 3, 64)
}

// Saves one sync to sync_metrics.csv. A logging problem never stops the sync itself.
func (app *App) logSyncMetrics(metrics SyncMetrics) {
	bytesSavedPercent := 0.0
	if metrics.FileSizeBytes > 0 {
		bytesSavedPercent = 100 * (1 - float64(metrics.ChunkBytesSent)/float64(metrics.FileSizeBytes))
	}
	row := []string{
		time.Now().Format(time.RFC3339),
		metrics.FileName,
		metrics.Mode,
		strconv.FormatInt(metrics.FileSizeBytes, 10),
		strconv.FormatInt(metrics.ChunkSizeBytes, 10),
		strconv.Itoa(metrics.TotalChunks),
		strconv.Itoa(metrics.ChunksSent),
		strconv.FormatInt(metrics.ChunkBytesSent, 10),
		strconv.FormatFloat(bytesSavedPercent, 'f', 2, 64),
		formatMilliseconds(metrics.PrepareTime),
		formatMilliseconds(metrics.TransferTime),
		formatMilliseconds(metrics.PrepareTime + metrics.TransferTime),
		formatMilliseconds(metrics.CPUTime),
		strconv.FormatInt(metrics.ChunkStorageBytes, 10),
		metrics.Outcome,
	}
	logFilePath := filepath.Join(app.performanceLogFolder(), "sync_metrics.csv")
	if logWritingError := appendCSVRow(logFilePath, syncMetricsHeader, row); logWritingError != nil {
		fmt.Println("Could not write performance log:", logWritingError)
	}
}

// Adds up the size of every file in a folder (used for the chunk storage folder)
func folderSizeBytes(folderPath string) int64 {
	var totalBytes int64
	filepath.WalkDir(folderPath, func(path string, entry os.DirEntry, walkError error) error {
		if walkError != nil || entry.IsDir() {
			return nil
		}
		if fileInfo, infoError := entry.Info(); infoError == nil {
			totalBytes += fileInfo.Size()
		}
		return nil
	})
	return totalBytes
}

// Turns the result of one outgoing sync into a row in sync_metrics.csv
func (app *App) recordSyncPerformance(fileName string, wholeFile bool, fileSizeBytes int64, chunkSizeBytes int64,
	totalChunks int, prepareTime time.Duration, transferStart time.Time, cpuTimeAtStart time.Duration,
	syncResult *SyncResult, syncError error) {

	syncMode := "incremental"
	if wholeFile {
		syncMode = "whole-file"
	}
	syncOutcome := "synced"
	chunksSent, chunkBytesSent := 0, int64(0)
	switch {
	case syncError != nil || syncResult == nil:
		syncOutcome = "failed"
	case syncResult.ConflictFile != "":
		syncOutcome = "conflict"
	case syncResult.KeepLocal:
		syncOutcome = "keep-local"
	}
	if syncResult != nil {
		chunksSent, chunkBytesSent = syncResult.ChunksSent, syncResult.ChunkBytesSent
	}

	app.logSyncMetrics(SyncMetrics{
		FileName:          fileName,
		Mode:              syncMode,
		FileSizeBytes:     fileSizeBytes,
		ChunkSizeBytes:    chunkSizeBytes,
		TotalChunks:       totalChunks,
		ChunksSent:        chunksSent,
		ChunkBytesSent:    chunkBytesSent,
		PrepareTime:       prepareTime,
		TransferTime:      time.Since(transferStart),
		CPUTime:           processCPUTime() - cpuTimeAtStart,
		ChunkStorageBytes: folderSizeBytes(app.chunkStoragePath),
		Outcome:           syncOutcome,
	})
}

var conflictMetricsHeader = []string{"time", "file", "file_size_bytes", "decision", "detection_ms"}

// Starts timing one conflict check on the receiving device.
// Call the returned function with the decision when the check is done.
func (app *App) startConflictCheckTimer(fileName string, fileSizeBytes int64) func(ConflictDecision) {
	checkStart := time.Now()
	return func(decision ConflictDecision) {
		row := []string{
			time.Now().Format(time.RFC3339),
			fileName,
			strconv.FormatInt(fileSizeBytes, 10),
			string(decision),
			formatMilliseconds(time.Since(checkStart)),
		}
		logFilePath := filepath.Join(app.performanceLogFolder(), "conflict_metrics.csv")
		if logWritingError := appendCSVRow(logFilePath, conflictMetricsHeader, row); logWritingError != nil {
			fmt.Println("Could not write conflict performance log:", logWritingError)
		}
	}
}
