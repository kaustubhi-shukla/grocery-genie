// Package database handles SQLite connection setup and schema migration.
//
// Public API:
//   - Open(path) opens (and creates if missing) the SQLite database file
//     and runs the schema. Returns a *sql.DB you can use across the app.
//
// The database file lives wherever the caller specifies (typically
// "grocery.db" in the working directory).
package database

import (
	"database/sql"
	_ "embed"
	"fmt"

	// Blank import: we don't call modernc.org/sqlite directly. Importing
	// it registers "sqlite" as a driver name with database/sql, so that
	// sql.Open("sqlite", ...) knows what to do.
	_ "modernc.org/sqlite"
)

// schemaSQL holds the contents of schema.sql at compile time.
// The //go:embed directive tells Go: "read this file and bake it into
// the binary as a string." That way we don't need schema.sql to exist
// on disk at runtime — it travels with the binary.
//
//go:embed schema.sql
var schemaSQL string

// Open connects to (or creates) the SQLite database at the given path,
// applies the schema, and returns a ready-to-use database handle.
//
// Caller is responsible for calling db.Close() when shutting down.
func Open(path string) (*sql.DB, error) {
	// sql.Open does NOT actually connect — it just prepares a connection
	// pool. The first real query triggers the connection.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database at %s: %w", path, err)
	}

	// Verify the connection works by pinging the database.
	// This catches issues like permission denied, disk full, etc.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// SQLite-specific tuning: enable foreign key enforcement.
	// (It is OFF by default in SQLite for backward compatibility.)
	// Without this, our REFERENCES and ON DELETE CASCADE rules are ignored.
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	// Run the schema. Since every CREATE uses "IF NOT EXISTS",
	// this is safe to call on every startup.
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	return db, nil
}
