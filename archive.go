// Author: Santosh Tripathee
// Archive for removed files.
// When a file is deleted, renamed or moved out of Synced Files, FileSync keeps
// an exact copy in the "Archive" folder instead of losing it. The copy is
// rebuilt from the stored chunks, checked against the SHA-256 hash in the
// database, and marked read-only. The database keeps the record of every
// archived file, and every archive action is written to archive_events.
// Archived files can be restored at any time, but can only be deleted for good
// after the retention period below.

package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// How long an archived file is locked before it can be deleted permanently.
const archiveRetentionDays = 30

// How long to wait after a delete or rename before archiving. Apps like Word
// save by renaming the old file and writing a new one with the same name, so
// we only archive if the file is still gone after this short wait.
const archiveCheckDelay = 1500 * time.Millisecond

// Integrity states of an archived copy.
const (
	archiveStatusOK       = "ok"       // copy is on disk and unchanged
	archiveStatusChanged  = "changed"  // copy was edited outside FileSync
	archiveStatusMissing  = "missing"  // copy was removed outside FileSync
	archiveStatusRestored = "restored" // copy was put back into Synced Files
	archiveStatusDeleted  = "deleted"  // copy was deleted after the lock ended
)

// ArchivedFile is one row of the archive, as the frontend sees it.
type ArchivedFile struct {
	ID                   int64  `json:"id"`
	FileName             string `json:"fileName"`
	OriginalPath         string `json:"originalPath"`
	SizeBytes            int64  `json:"sizeBytes"`
	Reason               string `json:"reason"`
	DeviceID             string `json:"deviceId"`
	ArchivedAt           string `json:"archivedAt"`
	RetainUntil          string `json:"retainUntil"`
	DaysUntilUnlock      int    `json:"daysUntilUnlock"`
	CanDeletePermanently bool   `json:"canDeletePermanently"`
	Status               string `json:"status"`
	MatchesOriginal      bool   `json:"matchesOriginal"`
}

// ArchiveCheckSummary is the result of checking every archived copy.
type ArchiveCheckSummary struct {
	Checked int `json:"checked"`
	OK      int `json:"ok"`
	Changed int `json:"changed"`
	Missing int `json:"missing"`
}

// Pending archive timers, one per file path, so several events for the
// same file (rename and remove) only archive it once.
var (
	archiveTimerMutex sync.Mutex
	archiveTimers     = map[string]*time.Timer{}
)

// createArchiveTables adds the archive tables next to the other FileSync tables.
func createArchiveTables(database *sql.DB) error {
	// archived_files: one row per archived copy, so a file deleted twice keeps both copies
	archivedFilesTable := `
	CREATE TABLE IF NOT EXISTS archived_files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_id TEXT NOT NULL,
		file_name TEXT NOT NULL,
		archive_path TEXT NOT NULL,
		size INTEGER NOT NULL,
		content_hash TEXT NOT NULL,
		matches_original INTEGER NOT NULL DEFAULT 0,
		reason TEXT NOT NULL,
		device_id TEXT,
		archived_at TEXT NOT NULL,
		retain_until TEXT NOT NULL,
		status TEXT NOT NULL,
		restored_as TEXT,
		finished_at TEXT
	);`

	// archive_events: history of everything that happened to archived files
	archiveEventsTable := `
	CREATE TABLE IF NOT EXISTS archive_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		archived_file_id INTEGER NOT NULL,
		event TEXT NOT NULL,
		details TEXT,
		device_id TEXT,
		created_at TEXT NOT NULL,
		FOREIGN KEY (archived_file_id) REFERENCES archived_files(id)
	);`

	if tableError := executeTableCreation(database, archivedFilesTable); tableError != nil {
		return tableError
	}
	return executeTableCreation(database, archiveEventsTable)
}

// archiveFolderPath is the Archive folder next to Synced Files.
func (app *App) archiveFolderPath() string {
	return filepath.Join(filepath.Dir(app.syncFolderPath), "Archive")
}

// localDeviceID returns this device's ID, or "" before registration.
func (app *App) localDeviceID() string {
	if app.deviceRegistry == nil {
		return ""
	}
	return app.deviceRegistry.Status().DeviceID
}

// scheduleArchiveCheck is called by the file watcher when a file is removed
// or renamed. It waits briefly, then archives the file only if it is really gone.
func (app *App) scheduleArchiveCheck(filePath string, reason string) {
	archiveTimerMutex.Lock()
	defer archiveTimerMutex.Unlock()

	if existingTimer, timerExists := archiveTimers[filePath]; timerExists {
		existingTimer.Stop()
	}
	archiveTimers[filePath] = time.AfterFunc(archiveCheckDelay, func() {
		archiveTimerMutex.Lock()
		delete(archiveTimers, filePath)
		archiveTimerMutex.Unlock()

		// The file is back (a normal save), so there is nothing to archive.
		if _, fileStatError := os.Stat(filePath); fileStatError == nil {
			return
		}

		app.fileProcessingMutex.Lock()
		defer app.fileProcessingMutex.Unlock()
		if archiveError := app.archiveRemovedFile(filePath, reason); archiveError != nil {
			fmt.Println("Archive error:", archiveError)
		}
	})
}

// archiveRemovedFile rebuilds the removed file from its chunks into the
// Archive folder, verifies it and records it in the database.
func (app *App) archiveRemovedFile(filePath string, reason string) error {
	if app.db == nil {
		return fmt.Errorf("database is not ready")
	}
	fileName := filepath.Base(filePath)
	relativePath, relativePathError := filepath.Rel(app.syncFolderPath, filePath)
	if relativePathError != nil {
		return relativePathError
	}

	// Hash of the last version recorded for this file, used to verify the copy.
	var recordedHash sql.NullString
	hashQueryError := app.db.QueryRow(`SELECT v.content_hash FROM files f
		JOIN file_versions v ON v.file_id = f.id AND v.version_number = f.current_version
		WHERE f.id = ? AND f.archived_at IS NULL ORDER BY v.id DESC LIMIT 1`, relativePath).Scan(&recordedHash)
	if hashQueryError == sql.ErrNoRows {
		// Either never processed, or already archived by an earlier event.
		var archivedAt sql.NullString
		app.db.QueryRow("SELECT archived_at FROM files WHERE id = ?", relativePath).Scan(&archivedAt)
		if archivedAt.Valid {
			return nil
		}
	} else if hashQueryError != nil {
		return fmt.Errorf("read recorded hash: %w", hashQueryError)
	}

	// Build the archive copy from the stored chunks.
	if folderCreationError := os.MkdirAll(app.archiveFolderPath(), 0755); folderCreationError != nil {
		return folderCreationError
	}
	chunkFolder := filepath.Join(app.chunkStoragePath, fileName)
	temporaryFile, temporaryFileError := os.CreateTemp(app.archiveFolderPath(), "archiving-*")
	if temporaryFileError != nil {
		return temporaryFileError
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath) // only matters if something below fails

	copySize, copyHash, rebuildError := writeChunksInOrder(chunkFolder, temporaryFile)
	temporaryFile.Close()
	if rebuildError != nil {
		// No usable chunks: fall back to the reconstructed copy, if there is one.
		copySize, copyHash, rebuildError = copyFileWithHash(filepath.Join(app.reconstructedFilesPath, fileName), temporaryPath)
		if rebuildError != nil {
			return fmt.Errorf("no stored copy of %s to archive: %w", fileName, rebuildError)
		}
	}
	matchesOriginal := recordedHash.Valid && recordedHash.String == copyHash

	// Give the copy a readable, unique name and make it read-only.
	archivedAt := time.Now()
	archivePath := uniquePath(app.archiveFolderPath(), archivedAt.Format("2006-01-02 15-04-05")+" "+fileName)
	if renameError := os.Rename(temporaryPath, archivePath); renameError != nil {
		return renameError
	}
	os.Chmod(archivePath, 0444)

	// Record the archive and mark the file as archived (never delete the row).
	deviceID := app.localDeviceID()
	databaseTransaction, transactionError := app.db.Begin()
	if transactionError != nil {
		return transactionError
	}
	defer databaseTransaction.Rollback()
	archivedAtText := archivedAt.UTC().Format(time.RFC3339)
	retainUntilText := archivedAt.Add(archiveRetentionDays * 24 * time.Hour).UTC().Format(time.RFC3339)
	insertResult, insertError := databaseTransaction.Exec(`INSERT INTO archived_files
		(file_id, file_name, archive_path, size, content_hash, matches_original, reason, device_id, archived_at, retain_until, status)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		relativePath, fileName, archivePath, copySize, copyHash, boolToInt(matchesOriginal), reason, deviceID, archivedAtText, retainUntilText, archiveStatusOK)
	if insertError != nil {
		return insertError
	}
	archiveID, _ := insertResult.LastInsertId()
	if _, markError := databaseTransaction.Exec("UPDATE files SET archived_at = ? WHERE id = ?", archivedAtText, relativePath); markError != nil {
		return markError
	}
	details := "Verified against the recorded version"
	if !matchesOriginal {
		details = "Saved from the last stored copy"
	}
	if eventError := logArchiveEvent(databaseTransaction, archiveID, "archived", details, deviceID); eventError != nil {
		return eventError
	}
	if commitError := databaseTransaction.Commit(); commitError != nil {
		return commitError
	}

	// The archive now holds the full copy, so the working chunks can go.
	os.RemoveAll(chunkFolder)
	os.Remove(filepath.Join(app.reconstructedFilesPath, fileName))

	fmt.Println("ARCHIVED:", fileName, "->", archivePath)
	app.notifyArchiveChange("Archived: " + fileName)
	return nil
}

// clearArchivedMark is called when a file shows up again under the same name,
// so its record counts as active again. Past archive copies are kept.
func (app *App) clearArchivedMark(fileName string) {
	if app.db == nil {
		return
	}
	app.db.Exec("UPDATE files SET archived_at = NULL WHERE id = ?", fileName)
}

// GetArchivedFiles lists the archived copies that can still be restored.
func (app *App) GetArchivedFiles() ([]ArchivedFile, error) {
	if app.db == nil {
		return nil, fmt.Errorf("database is not ready")
	}
	rows, queryError := app.db.Query(`SELECT id, file_id, file_name, size, reason, COALESCE(device_id, ''),
		archived_at, retain_until, status, matches_original
		FROM archived_files WHERE status IN (?, ?, ?) ORDER BY archived_at DESC`,
		archiveStatusOK, archiveStatusChanged, archiveStatusMissing)
	if queryError != nil {
		return nil, queryError
	}
	defer rows.Close()

	archivedFiles := []ArchivedFile{}
	now := time.Now()
	for rows.Next() {
		var archivedFile ArchivedFile
		var matchesOriginal int
		if scanError := rows.Scan(&archivedFile.ID, &archivedFile.OriginalPath, &archivedFile.FileName, &archivedFile.SizeBytes,
			&archivedFile.Reason, &archivedFile.DeviceID, &archivedFile.ArchivedAt, &archivedFile.RetainUntil,
			&archivedFile.Status, &matchesOriginal); scanError != nil {
			return nil, scanError
		}
		archivedFile.MatchesOriginal = matchesOriginal == 1
		retainUntil, _ := time.Parse(time.RFC3339, archivedFile.RetainUntil)
		if hoursLeft := retainUntil.Sub(now).Hours(); hoursLeft > 0 {
			archivedFile.DaysUntilUnlock = int(math.Ceil(hoursLeft / 24))
		}
		archivedFile.CanDeletePermanently = archivedFile.DaysUntilUnlock == 0
		archivedFiles = append(archivedFiles, archivedFile)
	}
	return archivedFiles, rows.Err()
}

// RestoreArchivedFile puts an archived copy back into Synced Files.
// It never overwrites a file: if the name is taken, it restores as
// "name (restored).ext". It returns the name the file was restored as.
func (app *App) RestoreArchivedFile(archiveID int64) (string, error) {
	archivePath, fileName, fileID, storedHash, status, loadError := app.loadArchiveRow(archiveID)
	if loadError != nil {
		return "", loadError
	}
	if status != archiveStatusOK {
		return "", fmt.Errorf("this copy is %s, so it can't be restored safely", status)
	}

	// Check the copy one more time right before restoring it.
	currentHash, hashError := hashStoredFile(archivePath)
	if hashError != nil || currentHash != storedHash {
		app.updateArchiveStatus(archiveID, archiveStatusChanged, "Copy changed before restore")
		return "", fmt.Errorf("the archived copy no longer matches its fingerprint, so it was not restored")
	}

	// Copy to a temporary file first, then move it in with one rename,
	// so the file watcher only sees the complete file.
	restoredPath := uniquePath(app.syncFolderPath, fileName, " (restored)")
	temporaryPath := filepath.Join(app.archiveFolderPath(), "restoring-"+strconv.FormatInt(archiveID, 10))
	if _, _, copyError := copyFileWithHash(archivePath, temporaryPath); copyError != nil {
		os.Remove(temporaryPath)
		return "", copyError
	}
	os.Chmod(temporaryPath, 0644)
	if renameError := os.Rename(temporaryPath, restoredPath); renameError != nil {
		os.Remove(temporaryPath)
		return "", renameError
	}

	// The file is back in Synced Files, so the archive copy is no longer needed.
	os.Chmod(archivePath, 0644)
	os.Remove(archivePath)
	restoredName := filepath.Base(restoredPath)
	app.db.Exec("UPDATE archived_files SET status = ?, restored_as = ?, finished_at = ? WHERE id = ?",
		archiveStatusRestored, restoredName, time.Now().UTC().Format(time.RFC3339), archiveID)
	if restoredName == fileName {
		app.clearArchivedMark(fileID)
	}
	app.logArchiveEventNow(archiveID, "restored", "Restored as "+restoredName)
	app.notifyArchiveChange("Restored: " + restoredName)
	return restoredName, nil
}

// DeleteArchivedFilePermanently deletes an archived copy, but only after
// the retention period has ended. The database record is kept.
func (app *App) DeleteArchivedFilePermanently(archiveID int64) error {
	archivePath, fileName, _, _, status, loadError := app.loadArchiveRow(archiveID)
	if loadError != nil {
		return loadError
	}
	if status == archiveStatusRestored || status == archiveStatusDeleted {
		return fmt.Errorf("this copy was already %s", status)
	}
	var retainUntilText string
	app.db.QueryRow("SELECT retain_until FROM archived_files WHERE id = ?", archiveID).Scan(&retainUntilText)
	retainUntil, _ := time.Parse(time.RFC3339, retainUntilText)
	if time.Now().Before(retainUntil) {
		return fmt.Errorf("%s is locked until %s", fileName, retainUntil.Local().Format("Jan 2, 2006"))
	}

	os.Chmod(archivePath, 0644)
	if removeError := os.Remove(archivePath); removeError != nil && !os.IsNotExist(removeError) {
		return removeError
	}
	app.db.Exec("UPDATE archived_files SET status = ?, finished_at = ? WHERE id = ?",
		archiveStatusDeleted, time.Now().UTC().Format(time.RFC3339), archiveID)
	app.logArchiveEventNow(archiveID, "deleted", "Deleted after the retention period")
	app.notifyArchiveChange("Permanently deleted: " + fileName)
	return nil
}

// CheckArchive re-hashes every archived copy and reports whether it is
// unchanged, changed or missing. It runs at startup and from the archive panel.
func (app *App) CheckArchive() (ArchiveCheckSummary, error) {
	summary := ArchiveCheckSummary{}
	if app.db == nil {
		return summary, fmt.Errorf("database is not ready")
	}
	type archiveRow struct {
		id                        int64
		archivePath, hash, status string
	}
	rows, queryError := app.db.Query("SELECT id, archive_path, content_hash, status FROM archived_files WHERE status IN (?, ?, ?)",
		archiveStatusOK, archiveStatusChanged, archiveStatusMissing)
	if queryError != nil {
		return summary, queryError
	}
	var archiveRows []archiveRow
	for rows.Next() {
		var row archiveRow
		if scanError := rows.Scan(&row.id, &row.archivePath, &row.hash, &row.status); scanError == nil {
			archiveRows = append(archiveRows, row)
		}
	}
	rows.Close()

	for _, row := range archiveRows {
		newStatus := archiveStatusOK
		currentHash, hashError := hashStoredFile(row.archivePath)
		if os.IsNotExist(hashError) {
			newStatus = archiveStatusMissing
		} else if hashError != nil || currentHash != row.hash {
			newStatus = archiveStatusChanged
		}
		summary.Checked++
		switch newStatus {
		case archiveStatusOK:
			summary.OK++
		case archiveStatusChanged:
			summary.Changed++
		case archiveStatusMissing:
			summary.Missing++
		}
		// Only log when something changed, so the history stays readable.
		if newStatus != row.status {
			app.updateArchiveStatus(row.id, newStatus, "Integrity check: "+newStatus)
		}
	}
	if summary.Changed > 0 || summary.Missing > 0 {
		app.notifyArchiveChange(fmt.Sprintf("Archive check: %d changed, %d missing", summary.Changed, summary.Missing))
	}
	return summary, nil
}

// loadArchiveRow reads the fields needed to restore or delete a copy.
func (app *App) loadArchiveRow(archiveID int64) (archivePath, fileName, fileID, contentHash, status string, loadError error) {
	if app.db == nil {
		return "", "", "", "", "", fmt.Errorf("database is not ready")
	}
	loadError = app.db.QueryRow("SELECT archive_path, file_name, file_id, content_hash, status FROM archived_files WHERE id = ?",
		archiveID).Scan(&archivePath, &fileName, &fileID, &contentHash, &status)
	if loadError == sql.ErrNoRows {
		loadError = fmt.Errorf("archived file not found")
	}
	return
}

// updateArchiveStatus saves a new integrity status and logs it.
func (app *App) updateArchiveStatus(archiveID int64, status string, details string) {
	app.db.Exec("UPDATE archived_files SET status = ? WHERE id = ?", status, archiveID)
	app.logArchiveEventNow(archiveID, status, details)
}

// logArchiveEventNow writes one history row outside a transaction.
func (app *App) logArchiveEventNow(archiveID int64, event string, details string) {
	app.db.Exec("INSERT INTO archive_events (archived_file_id, event, details, device_id, created_at) VALUES (?,?,?,?,?)",
		archiveID, event, details, app.localDeviceID(), time.Now().UTC().Format(time.RFC3339))
}

// logArchiveEvent writes one history row inside an open transaction.
func logArchiveEvent(databaseTransaction *sql.Tx, archiveID int64, event string, details string, deviceID string) error {
	_, insertError := databaseTransaction.Exec("INSERT INTO archive_events (archived_file_id, event, details, device_id, created_at) VALUES (?,?,?,?,?)",
		archiveID, event, details, deviceID, time.Now().UTC().Format(time.RFC3339))
	return insertError
}

// notifyArchiveChange tells the frontend to refresh the archive and activity list.
func (app *App) notifyArchiveChange(message string) {
	if app.ctx == nil {
		return
	}
	runtime.EventsEmit(app.ctx, "archive-change")
	runtime.EventsEmit(app.ctx, "file-change", message)
}

// writeChunksInOrder writes chunk_0, chunk_1, ... into the output file
// and returns the total size and the SHA-256 of everything written.
func writeChunksInOrder(chunkFolder string, output io.Writer) (int64, string, error) {
	folderEntries, folderReadError := os.ReadDir(chunkFolder)
	if folderReadError != nil {
		return 0, "", folderReadError
	}
	type chunkFile struct {
		index int
		path  string
	}
	var chunkFiles []chunkFile
	for _, folderEntry := range folderEntries {
		entryName := folderEntry.Name()
		if folderEntry.IsDir() || !strings.HasPrefix(entryName, "chunk_") || !strings.HasSuffix(entryName, ".chunk") {
			continue
		}
		chunkIndex, conversionError := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(entryName, "chunk_"), ".chunk"))
		if conversionError != nil {
			continue
		}
		chunkFiles = append(chunkFiles, chunkFile{chunkIndex, filepath.Join(chunkFolder, entryName)})
	}
	sort.Slice(chunkFiles, func(first, second int) bool { return chunkFiles[first].index < chunkFiles[second].index })

	contentHasher := sha256.New()
	destination := io.MultiWriter(output, contentHasher)
	var totalSize int64
	for position, chunk := range chunkFiles {
		// A gap in the numbering means a chunk is missing, so the copy would be wrong.
		if chunk.index != position {
			return 0, "", fmt.Errorf("chunk %d is missing", position)
		}
		chunkData, readError := os.ReadFile(chunk.path)
		if readError != nil {
			return 0, "", readError
		}
		written, writeError := destination.Write(chunkData)
		if writeError != nil {
			return 0, "", writeError
		}
		totalSize += int64(written)
	}
	return totalSize, hex.EncodeToString(contentHasher.Sum(nil)), nil
}

// copyFileWithHash copies a file and returns its size and SHA-256.
func copyFileWithHash(sourcePath string, destinationPath string) (int64, string, error) {
	sourceFile, openError := os.Open(sourcePath)
	if openError != nil {
		return 0, "", openError
	}
	defer sourceFile.Close()
	destinationFile, createError := os.Create(destinationPath)
	if createError != nil {
		return 0, "", createError
	}
	contentHasher := sha256.New()
	copiedSize, copyError := io.Copy(io.MultiWriter(destinationFile, contentHasher), sourceFile)
	closeError := destinationFile.Close()
	if copyError != nil {
		return 0, "", copyError
	}
	if closeError != nil {
		return 0, "", closeError
	}
	return copiedSize, hex.EncodeToString(contentHasher.Sum(nil)), nil
}

// uniquePath returns folder/name, or adds a label and number until the name is free.
// Example: report.docx, report (restored).docx, report (restored 2).docx
func uniquePath(folder string, fileName string, label ...string) string {
	candidate := filepath.Join(folder, fileName)
	if _, statError := os.Stat(candidate); os.IsNotExist(statError) {
		return candidate
	}
	// Default label is just a number: "report (2).docx"
	labelText := ""
	if len(label) > 0 {
		labelText = label[0]
	}
	extension := filepath.Ext(fileName)
	baseName := strings.TrimSuffix(fileName, extension)
	for number := 1; ; number++ {
		var nameTag string
		switch {
		case labelText == "":
			nameTag = fmt.Sprintf(" (%d)", number+1)
		case number == 1:
			nameTag = labelText
		default:
			nameTag = strings.TrimSuffix(labelText, ")") + fmt.Sprintf(" %d)", number)
		}
		candidate = filepath.Join(folder, baseName+nameTag+extension)
		if _, statError := os.Stat(candidate); os.IsNotExist(statError) {
			return candidate
		}
	}
}

// boolToInt stores a true/false value in SQLite.
func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
