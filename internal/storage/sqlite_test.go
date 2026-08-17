package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Every pooled connection must carry the pragmas, not just the first one
// database/sql happened to hand out during Open. Holding several *sql.Conn at
// once forces the pool to create distinct connections instead of reusing one.
func TestOpenAppliesPragmasToEveryPooledConnection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	db, err := Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	const connections = 4
	db.SetMaxOpenConns(connections)

	ctx := context.Background()
	held := make([]*sql.Conn, 0, connections)
	for i := 0; i < connections; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
		defer conn.Close()
		held = append(held, conn)
	}

	for i, conn := range held {
		var foreignKeys int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("conn %d read foreign_keys: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("conn %d: foreign_keys = %d, want 1", i, foreignKeys)
		}

		var busyTimeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("conn %d read busy_timeout: %v", i, err)
		}
		if busyTimeout != 5000 {
			t.Errorf("conn %d: busy_timeout = %d, want 5000", i, busyTimeout)
		}

		var journalMode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatalf("conn %d read journal_mode: %v", i, err)
		}
		if journalMode != "wal" {
			t.Errorf("conn %d: journal_mode = %q, want %q", i, journalMode, "wal")
		}

		// The pragma readout is only worth something if it changes behaviour:
		// a chunk pointing at a note that does not exist must be rejected.
		_, err := conn.ExecContext(ctx,
			`INSERT INTO chunks (note_id, content, chunk_index, embedding) VALUES (?, ?, ?, ?)`,
			9999, "orphan", 0, []byte{})
		if err == nil {
			t.Errorf("conn %d: insert with dangling note_id succeeded, foreign keys are not enforced", i)
		}
	}
}
