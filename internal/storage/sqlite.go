package storage

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo toolchain needed
)

const schema = `
CREATE TABLE IF NOT EXISTS notes (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	title      TEXT NOT NULL,
	path       TEXT NOT NULL UNIQUE,
	content    TEXT NOT NULL,
	note_type  TEXT NOT NULL DEFAULT 'note',
	done       INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chunks (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	note_id     INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
	content     TEXT NOT NULL,
	chunk_index INTEGER NOT NULL,
	embedding   BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chunks_note_id ON chunks(note_id);
`

func Open(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// Quirk worth knowing: SQLite has foreign keys OFF by default, and it's
	// a per-connection setting, not a database-level one — so this has to
	// run every time we open a connection, not just once at DB creation.
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	if err := migrateNotesColumns(db); err != nil {
		return nil, fmt.Errorf("migrate notes table: %w", err)
	}

	return db, nil
}

// migrateNotesColumns adds columns introduced after the initial schema.
// CREATE TABLE IF NOT EXISTS only helps on a fresh DB; an existing notes
// table from a prior run needs ALTER TABLE, guarded by checking which
// columns are already there so re-running this is always safe.
func migrateNotesColumns(db *sql.DB) error {
	existing := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(notes)`)
	if err != nil {
		return fmt.Errorf("inspect notes columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	wanted := []struct{ name, ddl string }{
		{"archived", "ALTER TABLE notes ADD COLUMN archived INTEGER NOT NULL DEFAULT 0"},
		{"archived_from", "ALTER TABLE notes ADD COLUMN archived_from TEXT NOT NULL DEFAULT ''"},
		{"trashed_from", "ALTER TABLE notes ADD COLUMN trashed_from TEXT NOT NULL DEFAULT ''"},
	}
	for _, w := range wanted {
		if existing[w.name] {
			continue
		}
		if _, err := db.Exec(w.ddl); err != nil {
			return fmt.Errorf("add column %s: %w", w.name, err)
		}
	}
	return nil
}
