// Santosh Tripathee
// Performance evaluation for FileSync records synchronization and conflict metrics.
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

// SyncMetrics describes one outgoing synchronization measurement.
type SyncMetrics struct {
	FileName          string
	Mode              string
	FileSizeBytes     int64
	ChunkSizeBytes    int64
	TotalChunks       int
	ChunksSent        int
	ChunkBytesSent    int64
	PrepareTime       time.Duration
	TransferTime      time.Duration
	CPUTime           time.Duration
	ChunkStorageBytes int64
	Outcome           string
}

var performanceLogMutex sync.Mutex

var syncMetricsHeader = []string{
	"time", "file", "mode", "file_size_bytes", "chunk_size_bytes",
	"total_chunks", "chunks_sent", "chunk_bytes_sent", "bytes_saved_percent",
	"prepare_ms", "transfer_ms", "total_ms", "cpu_ms", "chunk_storage_bytes", "outcome",
}

var conflictMetricsHeader = []string{"time", "file", "file_size_bytes", "decision", "detection_ms"}

// performanceLogFolder returns the runtime metrics folder beside Synced Files.
func (app *App) performanceLogFolder() string {
	return filepath.Join(filepath.Dir(app.syncFolderPath), "Performance Logs")
}

// appendCSVRow safely appends one row and creates the header for a new file.
func appendCSVRow(logFilePath string, header []string, row []string) error {
	performanceLogMutex.Lock()
	defer performanceLogMutex.Unlock()

	if folderCreationError := os.MkdirAll(filepath.Dir(logFilePath), 0755); folderCreationError != nil {
		return folderCreationError
	}
	_, fileStatError := os.Stat(logFilePath)
	isNewLogFile := os.IsNotExist(fileStatError)
	if fileStatError != nil && !isNewLogFile {
		return fileStatError
	}

	logFile, fileOpenError := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if fileOpenError != nil {
		return fileOpenError
	}
	defer logFile.Close()

	csvWriter := csv.NewWriter(logFile)
	if isNewLogFile {
		if headerWritingError := csvWriter.Write(header); headerWritingError != nil {
			return headerWritingError
		}
	}
	if rowWritingError := csvWriter.Write(row); rowWritingError != nil {
		return rowWritingError
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

// formatMilliseconds formats a duration with three decimal places.
func formatMilliseconds(duration time.Duration) string {
	return strconv.FormatFloat(float64(duration.Microseconds())/1000, 'f', 3, 64)
}

// logSyncMetrics saves one outgoing synchronization result.
func (app *App) logSyncMetrics(metrics SyncMetrics) {
	// Leave savings empty for failed syncs because no transfer was completed.
	bytesSavedPercent := ""
	if metrics.Outcome != "failed" && metrics.FileSizeBytes > 0 {
		savedRatio := 1 - float64(metrics.ChunkBytesSent)/float64(metrics.FileSizeBytes)
		bytesSavedPercent = strconv.FormatFloat(100*savedRatio, 'f', 2, 64)
	}
	row := []string{
		time.Now().Format(time.RFC3339), metrics.FileName, metrics.Mode,
		strconv.FormatInt(metrics.FileSizeBytes, 10), strconv.FormatInt(metrics.ChunkSizeBytes, 10),
		strconv.Itoa(metrics.TotalChunks), strconv.Itoa(metrics.ChunksSent),
		strconv.FormatInt(metrics.ChunkBytesSent, 10), bytesSavedPercent,
		formatMilliseconds(metrics.PrepareTime), formatMilliseconds(metrics.TransferTime),
		formatMilliseconds(metrics.PrepareTime + metrics.TransferTime), formatMilliseconds(metrics.CPUTime),
		strconv.FormatInt(metrics.ChunkStorageBytes, 10), metrics.Outcome,
	}
	if logWritingError := appendCSVRow(filepath.Join(app.performanceLogFolder(), "sync_metrics.csv"), syncMetricsHeader, row); logWritingError != nil {
		fmt.Println("Could not write performance log:", logWritingError)
	}
}

// folderSizeBytes totals regular files below a folder for storage measurements.
func folderSizeBytes(folderPath string) int64 {
	var totalBytes int64
	_ = filepath.WalkDir(folderPath, func(_ string, entry os.DirEntry, walkError error) error {
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

// recordSyncPerformance converts an outgoing result into a CSV measurement.
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
		FileName: fileName, Mode: syncMode, FileSizeBytes: fileSizeBytes, ChunkSizeBytes: chunkSizeBytes,
		TotalChunks: totalChunks, ChunksSent: chunksSent, ChunkBytesSent: chunkBytesSent,
		PrepareTime: prepareTime, TransferTime: time.Since(transferStart), CPUTime: processCPUTime() - cpuTimeAtStart,
		ChunkStorageBytes: folderSizeBytes(app.chunkStoragePath), Outcome: syncOutcome,
	})
}

// startConflictCheckTimer records how long one conflict decision takes.
func (app *App) startConflictCheckTimer(fileName string, fileSizeBytes int64) func(ConflictDecision) {
	checkStart := time.Now()
	return func(decision ConflictDecision) {
		row := []string{time.Now().Format(time.RFC3339), fileName, strconv.FormatInt(fileSizeBytes, 10), string(decision), formatMilliseconds(time.Since(checkStart))}
		if logWritingError := appendCSVRow(filepath.Join(app.performanceLogFolder(), "conflict_metrics.csv"), conflictMetricsHeader, row); logWritingError != nil {
			fmt.Println("Could not write conflict performance log:", logWritingError)
		}
	}
}
