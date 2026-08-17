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

CREATE TABLE IF NOT EXISTS action_audit (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	action_type TEXT NOT NULL,
	action_json TEXT NOT NULL,
	outcome     TEXT NOT NULL,
	message     TEXT NOT NULL DEFAULT '',
	error       TEXT NOT NULL DEFAULT '',
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_action_audit_created_at ON action_audit(created_at DESC);

CREATE TABLE IF NOT EXISTS jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL,
 payload TEXT NOT NULL DEFAULT '{}',
 status TEXT NOT NULL DEFAULT 'queued',
 progress_current INTEGER NOT NULL DEFAULT 0,
 progress_total INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_jobs_status_created ON jobs(status, created_at);

-- A small, personal cache. It is deliberately separate from notes: metadata
-- can be reused even when a book note is moved or deleted.
CREATE TABLE IF NOT EXISTS book_metadata (
	title_key TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	authors_json TEXT NOT NULL DEFAULT '[]',
	genres_json TEXT NOT NULL DEFAULT '[]',
	published_year INTEGER NOT NULL DEFAULT 0,
	isbn TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL,
	verified_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// connectionPragmas is a DSN query string, not a one-shot statement, and that
// distinction is the whole point. database/sql keeps a *pool* of connections
// and opens new ones on demand, while SQLite's foreign_keys and busy_timeout
// are per-connection settings. A `PRAGMA ...` executed once after Open lands on
// whichever pooled connection happened to serve it, so a later query on a
// second connection would silently run with foreign keys OFF. The
// modernc.org/sqlite driver applies every `_pragma=` DSN parameter inside its
// connection constructor, so putting them here is what makes them hold on all
// connections, forever.
//
//   - foreign_keys: SQLite defaults them OFF; chunks.note_id ON DELETE CASCADE
//     is only real with them ON.
//   - busy_timeout: without it a concurrent writer fails instantly with
//     SQLITE_BUSY. 5s of waiting is what the UI, engine, and indexer overlapping
//     on one file need.
//   - journal_mode WAL: readers no longer block the writer and vice versa. WAL
//     is persisted in the database file, so this is a no-op after the first
//     open — it stays listed so a fresh database gets it too.
const connectionPragmas = "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"

func Open(dbPath string) (*sql.DB, error) {
	// The driver splits the DSN at the first '?' and keeps only the prefix as
	// the filename, so a plain path (or ":memory:") carries the pragmas fine.
	db, err := sql.Open("sqlite", dbPath+"?"+connectionPragmas)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
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
