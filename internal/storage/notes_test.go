package storage

import (
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/models"
)

// AllMeta feeds the retrieval catalog, so it is bound by the same rule as All:
// a trashed note must never reach RAG or injected vault context.
func TestAllMetaExcludesTrashedNotes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, err := Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := NewNoteStore(db)
	if _, err := store.Create(&models.Note{Title: "Live", Path: "/vault/live.md", Content: "body", Type: models.NoteTypeNote}); err != nil {
		t.Fatalf("create live note: %v", err)
	}
	if _, err := store.Create(&models.Note{Title: "Deleted", Path: "/vault/.trash/deleted.md", Content: "body", Type: models.NoteTypeTask, Done: true, TrashedFrom: "deleted.md"}); err != nil {
		t.Fatalf("create trashed note: %v", err)
	}

	meta, err := store.AllMeta()
	if err != nil {
		t.Fatalf("all meta: %v", err)
	}
	if len(meta) != 1 {
		t.Fatalf("AllMeta returned %d notes, want only the live one: %+v", len(meta), meta)
	}
	if meta[0].Title != "Live" || meta[0].Path != "/vault/live.md" || meta[0].Type != models.NoteTypeNote {
		t.Fatalf("AllMeta entry = %+v", meta[0])
	}
}
