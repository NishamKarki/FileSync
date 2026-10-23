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
	database, databaseOpenError := sql.Open("sqlite", dbPath)
	if databaseOpenError != nil {
		return nil, databaseOpenError
	}

	// Check that the connection actually works
	pingError := database.Ping()
	if pingError != nil {
		return nil, pingError
	}

	// Create the tables id that do not exists yet
	tableError := createTables(database)
	if tableError != nil {
		return nil, tableError
	}

	// Add the content_hash column to older databases that don't have it yet
	columnError := addContentHashColumn(database)
	if columnError != nil {
		return nil, columnError
	}

	return database, nil
}

// Creates the three tables fileSync needs : files, file_versions, and chunks
func createTables(database *sql.DB) error {

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
		content_hash TEXT,
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

	// Create the files table.
	filesTableCreationError :=
		executeTableCreation(
			database,
			filesTable,
		)

	if filesTableCreationError != nil {
		return filesTableCreationError
	}

	// Create the file_versions table.
	versionsTableCreationError :=
		executeTableCreation(
			database,
			versionsTable,
		)

	if versionsTableCreationError != nil {
		return versionsTableCreationError
	}

	// Create the chunks table.
	chunksTableCreationError :=
		executeTableCreation(
			database,
			chunksTable,
		)

	if chunksTableCreationError != nil {
		return chunksTableCreationError
	}

	return nil
}

// executeTableCreation runs a CREATE TABLE statement.
//
// The table definitions use IF NOT EXISTS, so running this function
// every time the application starts does not remove existing data.
func executeTableCreation(
	database *sql.DB,
	tableQuery string,
) error {

	_, tableExecutionError :=
		database.Exec(tableQuery)

	return tableExecutionError
}

// Databases created before conflict detection don't have the content_hash
// column. CREATE TABLE IF NOT EXISTS skips tables that already exist, so it
// can't add a new column to them. This checks whether the column is there
// and adds it only if it's missing. Existing data is not changed.
func addContentHashColumn(database *sql.DB) error {

	// pragma_table_info lists the columns of a table.
	// Count how many of them are named content_hash (0 or 1).
	var columnCount int
	checkError := database.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('file_versions') WHERE name = 'content_hash'",
	).Scan(&columnCount)
	if checkError != nil {
		return checkError
	}

	// The column already exists, so there's nothing to do
	if columnCount > 0 {
		return nil
	}

	// Add the missing column. Older versions will have no value (NULL) in it.
	_, alterError := database.Exec(
		"ALTER TABLE file_versions ADD COLUMN content_hash TEXT",
	)
	return alterError
}
