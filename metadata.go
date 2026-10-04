////// Aurthor: Santosh Tripathee

package main

import (
	"database/sql"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Sets up the metadata database. Creates the file if it doesn't already exist.
func InitDatabase(syncFolderPath string) (*sql.DB, error) {

	// Store the database file next to the synced file folder
	dbPath := filepath.Join(filepath.Dir(syncFolderPath), "filesync.db")

	// Open a connection to the database (creates the file if missing)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Check that the connection actually works
	pingError := db.Ping()
	if pingError != nil {
		return nil, pingError
	}

	// Create the tables id that do not exists yet
	tableError := createTables(db)
	if tableError != nil {
		return nil, tableError
	}

	return db, nil
}

// Creates the three tables fileSync needs : files, file_versions, and chunks
func createTables(db *sql.DB) error {

	// files: one row per synced file, tracking its current version
	filesTable := `
	CREATE TABLE IF NOT EXISTS files (
		id TEXT PRIMARY KEY,
		path TEXT NOT NULL,
		name TEXT NOT NULL,
		current_version INTEGER NOT NULL DEFAULT 1,
		size INTEGER,
		modified_by TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	// file_versions: every version of every file, inclusing which version
	// it came from (based_on_version is what powers conflict detection)
	versionsTable := `
	CREATE TABLE IF NOT EXISTS file_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_id TEXT NOT NULL,
		version_number INTEGER NOT NULL,
		based_on_version INTEGER,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		device_id TEXT,
		FOREIGN KEY (file_id) REFERENCES files(id)
	);`

	// chunks: the chunk hashes that make up each version, in order
	chunksTable := `
	CREATE TABLE IF NOT EXISTS chunks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_version_id INTEGER NOT NULL,
		chunk_index INTEGER NOT NULL,
		chunk_hash TEXT NOT NULL,
		FOREIGN KEY (file_version_id) REFERENCES file_versions(id)
	);`

	// Run each one separately. IF NOT EXISTS means this is safe to run
	//  every time the app starts without wiping existing data.
	_, err := db.Exec(filesTable)
	if err != nil {
		return err
	}

	_, err = db.Exec(versionsTable)
	if err != nil {
		return err
	}

	_, err = db.Exec(chunksTable)
	if err != nil {
		return err
	}

	return nil

}
