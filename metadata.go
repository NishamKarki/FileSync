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

	// Santosh Tripathee's conflict metadata migrations, integrated with the registry.
	for _, column := range [][2]string{{"file_versions", "content_hash"}, {"files", "last_synced_hash"}} {
		if metadataColumnMigrationError := addColumnIfMissing(database, column[0], column[1], "TEXT"); metadataColumnMigrationError != nil {
			database.Close()
			return nil, metadataColumnMigrationError
		}
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
		last_synced_hash TEXT,
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
	// Device registration extends Santosh's metadata database without changing
	// the existing files, file_versions, or chunks tables.
	if deviceRegistryTableCreationError := executeTableCreation(database, `CREATE TABLE IF NOT EXISTS device_registry (
		registration_key TEXT NOT NULL UNIQUE,
		device_id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		ip TEXT NOT NULL,
		port INTEGER NOT NULL,
		last_seen INTEGER NOT NULL
	)`); deviceRegistryTableCreationError != nil {
		return deviceRegistryTableCreationError
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

// Author: Santosh Tripathee. Integration: Rabindra Neupane.
// Rabindra Neupane
// Databases created before conflict detection are missing some newer columns.
// CREATE TABLE IF NOT EXISTS skips tables that already exist, so it can't add
// a new column to them. This checks whether the column is there and adds it
// only if it's missing. Existing data is not changed.
func addColumnIfMissing(database *sql.DB, tableName string, columnName string, columnType string) error {

	// pragma_table_info lists the columns of a table.
	// Count how many of them have this name (0 or 1).
	var columnCount int
	checkError := database.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?",
		tableName, columnName,
	).Scan(&columnCount)
	if checkError != nil {
		return checkError
	}

	// The column already exists, so there's nothing to do
	if columnCount > 0 {
		return nil
	}

	// Add the missing column. Existing rows will have no value (NULL) in it.
	// Table and column names can't be passed as ? values in SQL, so they are
	// put into the text directly. They only ever come from our own code.
	_, alterError := database.Exec(
		"ALTER TABLE " + tableName + " ADD COLUMN " + columnName + " " + columnType,
	)
	return alterError
}
