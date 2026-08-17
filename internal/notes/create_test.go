package notes

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/utils"
)

func newTestService(t *testing.T) (*Service, string) {
	service, vault, _ := newTestServiceWithDB(t)
	return service, vault
}

func newTestServiceWithDB(t *testing.T) (*Service, string, *sql.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), bookTestEmbedder{}), vault, db
}

// breakNoteWrites makes every INSERT or UPDATE on notes fail from that point
// on. A trigger on the real database is the smallest way to reach the "file
// written, row refused" branch: a fake store would have to imitate NoteStore
// and could drift from it.
func breakNoteWrites(t *testing.T, db *sql.DB, event string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TRIGGER refuse_` + event + ` BEFORE ` + event + ` ON notes BEGIN SELECT RAISE(ABORT, 'note write refused'); END`); err != nil {
		t.Fatalf("install failing trigger: %v", err)
	}
}

// V-04: a file created for a row that never landed is invisible to Athena and
// will collide with the next create of the same title.
func TestCreateNoteRemovesFileWhenSaveFails(t *testing.T) {
	service, vault, db := newTestServiceWithDB(t)
	breakNoteWrites(t, db, "INSERT")

	_, created, err := service.CreateNote(t.Context(), "Orphan Risk", "body", "", nil)
	if created || err == nil {
		t.Fatalf("create with a failing insert: created=%t err=%v", created, err)
	}
	path, err := utils.NotePath(vault, "", "Orphan Risk")
	if err != nil {
		t.Fatalf("build note path: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("failed insert left an orphan file at %s (stat: %v)", path, statErr)
	}
}

// V-04: the mirror case — a moved file whose row never moved leaves the DB
// pointing at a path that no longer holds the note.
func TestMoveNoteMovesFileBackWhenUpdateFails(t *testing.T) {
	service, vault, db := newTestServiceWithDB(t)
	if err := utils.EnsureDir(vault, "work"); err != nil {
		t.Fatalf("create destination folder: %v", err)
	}
	note, created, err := service.CreateNote(t.Context(), "Stale Path", "body", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	originalPath := note.Path
	breakNoteWrites(t, db, "UPDATE")

	if _, err := service.MoveNote(t.Context(), note.ID, "work"); err == nil {
		t.Fatal("expected the refused row update to be reported")
	}
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("file was not moved back to %s: %v", originalPath, err)
	}
	movedPath, err := utils.NotePath(vault, "work", "Stale Path")
	if err != nil {
		t.Fatalf("build moved path: %v", err)
	}
	if _, err := os.Stat(movedPath); !os.IsNotExist(err) {
		t.Fatalf("file stayed in the destination folder (stat: %v)", err)
	}
	stored, err := service.GetNote(note.ID)
	if err != nil || stored.Path != originalPath {
		t.Fatalf("stored path = %q, want %q (err=%v)", stored.Path, originalPath, err)
	}
}

func TestCreateNoteRejectsDifferentTitleOnSameSlug(t *testing.T) {
	service, _ := newTestService(t)

	first, created, err := service.CreateNote(t.Context(), "Go Slices", "original body", "", nil)
	if err != nil || !created {
		t.Fatalf("create first note: created=%t err=%v", created, err)
	}

	_, created, err = service.CreateNote(t.Context(), "Go: Slices", "different body", "", nil)
	if created || err == nil {
		t.Fatalf("colliding title: created=%t err=%v, want error", created, err)
	}
	if !strings.Contains(err.Error(), "Go: Slices") || !strings.Contains(err.Error(), "Go Slices") {
		t.Fatalf("error %q must name both titles", err)
	}

	// The first note's content must survive the rejected create.
	raw, err := utils.ReadNoteFile(first.Path)
	if err != nil || !strings.Contains(raw, "original body") {
		t.Fatalf("first note body: raw=%q err=%v", raw, err)
	}

	// Re-creating the SAME title stays idempotent for re-emitting models.
	same, created, err := service.CreateNote(t.Context(), "Go Slices", "ignored body", "", nil)
	if err != nil || created || same.ID != first.ID {
		t.Fatalf("re-create same title: note=%+v created=%t err=%v", same, created, err)
	}
}

func TestCreateNoteRequiresExistingFolder(t *testing.T) {
	service, vault := newTestService(t)

	_, created, err := service.CreateNote(t.Context(), "Weak Model Guess", "body", "invented/folder", nil)
	if created || err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing folder: created=%t err=%v", created, err)
	}
	exists, err := utils.FolderExists(vault, "invented")
	if err != nil {
		t.Fatalf("check vault: %v", err)
	}
	if exists {
		t.Fatal("create must not invent the destination folder")
	}
}

func TestCreateBookDefaultsToReadingFolder(t *testing.T) {
	service, vault := newTestService(t)

	book, created, err := service.CreateBook(t.Context(), models.BookMetadata{Title: "Foundation"}, "", time.Now())
	if err != nil || !created {
		t.Fatalf("create book: created=%t err=%v", created, err)
	}
	if got, want := utils.RelVault(vault, book.Path), "books/reading/foundation.md"; got != want {
		t.Fatalf("book path = %q, want %q", got, want)
	}
}

func TestCreateBookKeepsNamedExistingFolder(t *testing.T) {
	service, vault := newTestService(t)
	if err := utils.EnsureDir(vault, "books/finished"); err != nil {
		t.Fatalf("create folder: %v", err)
	}

	book, created, err := service.CreateBook(t.Context(), models.BookMetadata{Title: "Foundation"}, "books/finished", time.Now())
	if err != nil || !created {
		t.Fatalf("create book: created=%t err=%v", created, err)
	}
	if got, want := utils.RelVault(vault, book.Path), "books/finished/foundation.md"; got != want {
		t.Fatalf("book path = %q, want %q", got, want)
	}
}
