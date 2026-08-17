package retrieval

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/storage"
)

// get_note is the by-ID read path that the catalog and semantic-search trash
// filters do not cover. It stays reachable in the run that trashed the note,
// because the model is still holding the pre-trash note_id, so a missing
// filter here quotes a soft-deleted body straight back at the user.
func TestNoteByIDRefusesTrashedNote(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	notes := storage.NewNoteStore(db)
	liveID, err := notes.Create(&models.Note{
		Title: "Launch plan", Path: filepath.Join(vault, "launch.md"), Content: "ship it",
	})
	if err != nil {
		t.Fatalf("create live note: %v", err)
	}
	trashedID, err := notes.Create(&models.Note{
		Title: "Old launch plan", Path: filepath.Join(vault, ".trash", "old.md"),
		Content: "the salary review numbers", TrashedFrom: "old.md",
	})
	if err != nil {
		t.Fatalf("create trashed note: %v", err)
	}

	service := NewService(vault, notes, storage.NewChunkStore(db), nil)

	view, err := service.NoteByID(trashedID)
	if view != nil {
		t.Fatalf("trashed note handed back to the model: %+v", view)
	}
	if err == nil {
		t.Fatal("reading a trashed note by ID returned no error; the model would retry the same ID")
	}
	if !strings.Contains(err.Error(), "trash") {
		t.Fatalf("error does not tell the model the note is trashed: %v", err)
	}
	// The error text reaches the model too, so it must not carry the body.
	if strings.Contains(err.Error(), "salary review") {
		t.Fatalf("trashed content leaked through the error: %v", err)
	}

	live, err := service.NoteByID(liveID)
	if err != nil || live == nil || live.Content != "ship it" {
		t.Fatalf("live note read broke: view=%+v err=%v", live, err)
	}

	// An unknown ID must stay a nil miss so callers can still say "not found"
	// rather than blaming the trash.
	missing, err := service.NoteByID(liveID + 9999)
	if err != nil || missing != nil {
		t.Fatalf("unknown ID = (%+v, %v), want (nil, nil)", missing, err)
	}
}
