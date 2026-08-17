package retrieval

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/storage"
)

func TestWikiTargetsAcceptsAliasesAndPaths(t *testing.T) {
	targets := wikiTargets("See [[books/foundation|Foundation]] and [[Plan]].")
	if !targets["foundation"] || !targets["plan"] {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestNormalizeTitleForDuplicates(t *testing.T) {
	if normalize("Go: Notes!") != normalize("go notes") {
		t.Fatal("equivalent titles did not normalize together")
	}
}

// get_notes is the batch twin of get_note and the same trust boundary: the
// model reaches it holding a note_id captured before the note was trashed, so
// without the shared guard a soft-deleted body goes straight into context.
func TestNotesByIDAndLinksRefuseTrashedNote(t *testing.T) {
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

	views, err := service.NotesByID([]int64{liveID, trashedID})
	for _, view := range views {
		if view.ID == trashedID || strings.Contains(view.Content, "salary review") {
			t.Fatalf("trashed note handed back to the model: %+v", view)
		}
	}
	if err == nil {
		t.Fatal("batch read of a trashed ID returned no error; the model would keep reusing the ID")
	}
	if !strings.Contains(err.Error(), "trash") || !strings.Contains(err.Error(), fmt.Sprint(trashedID)) {
		t.Fatalf("error does not name the trashed ID: %v", err)
	}
	// The error text reaches the model too, so it must not carry the body.
	if strings.Contains(err.Error(), "salary review") {
		t.Fatalf("trashed content leaked through the error: %v", err)
	}

	// Live-only batches, and unknown IDs inside them, keep working.
	views, err = service.NotesByID([]int64{liveID, liveID + 9999})
	if err != nil || len(views) != 1 || views[0].Content != "ship it" {
		t.Fatalf("live batch read broke: views=%+v err=%v", views, err)
	}

	// get_note_links reports a trashed note's outgoing links, which are parsed
	// from its soft-deleted body.
	if _, err := service.Links(trashedID); err == nil || !strings.Contains(err.Error(), "trash") {
		t.Fatalf("links for a trashed note = %v, want a trashed error", err)
	}
	if _, err := service.Links(liveID); err != nil {
		t.Fatalf("links for a live note broke: %v", err)
	}
}
