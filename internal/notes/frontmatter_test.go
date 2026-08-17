package notes

import (
	"strings"
	"testing"
	"time"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// readRaw is the file exactly as it sits on disk, for the assertions about a
// key being absent rather than false.
func readRaw(t *testing.T, path string) string {
	t.Helper()
	raw, err := utils.ReadNoteFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// readFrontmatter reads what Obsidian would read: the file, never the row.
func readFrontmatter(t *testing.T, path string) parser.Frontmatter {
	t.Helper()
	raw, err := utils.ReadNoteFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	frontmatter, _, err := parser.ParseMarkdown(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return frontmatter
}

// V-06: a rename changed the filename and the SQLite row but never the file's
// YAML, so Obsidian kept displaying the old title forever.
func TestRenameNoteUpdatesFrontmatterTitle(t *testing.T) {
	service, _ := newTestService(t)

	note, created, err := service.CreateNote(t.Context(), "Old Name", "body", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	renamed, err := service.RenameNote(note.ID, "New Name")
	if err != nil {
		t.Fatalf("rename note: %v", err)
	}

	frontmatter := readFrontmatter(t, renamed.Path)
	if frontmatter.Title != "New Name" {
		t.Fatalf("YAML title = %q, want %q", frontmatter.Title, "New Name")
	}
	// The body must survive the rewrite untouched — the file has to stay a
	// normal Markdown note, not a rendered projection of the database.
	body, err := service.readNoteBody(renamed.Path)
	if err != nil || body != "body" {
		t.Fatalf("body = %q err=%v, want %q", body, err, "body")
	}
}

// V-06: type lived only in SQLite, so a task was indistinguishable from any
// other note in the vault and the distinction died with the database.
func TestCreateTaskWritesTypeIntoFrontmatter(t *testing.T) {
	service, _ := newTestService(t)

	task, created, err := service.CreateTask(t.Context(), "Ship V-06", "", "")
	if err != nil || !created {
		t.Fatalf("create task: created=%t err=%v", created, err)
	}
	if got := readFrontmatter(t, task.Path).Kind; got != string(models.NoteTypeTask) {
		t.Fatalf("YAML kind = %q, want %q", got, models.NoteTypeTask)
	}
	stored, err := service.GetNote(task.ID)
	if err != nil || stored.Type != models.NoteTypeTask {
		t.Fatalf("stored type = %q err=%v", stored.Type, err)
	}
}

// V-06: a ticked task was a boolean in SQLite and nothing else. Every task
// looked identical in Obsidian, and rebuilding the database from the vault
// reopened every finished one.
func TestMarkDoneWritesDoneIntoFrontmatter(t *testing.T) {
	service, _ := newTestService(t)

	task, created, err := service.CreateTask(t.Context(), "Ship V-06", "body", "")
	if err != nil || !created {
		t.Fatalf("create task: created=%t err=%v", created, err)
	}
	// A new task is open, and "open" is the absence of the key — otherwise every
	// note Athena writes grows a `done: false` line it never needed.
	if raw := readRaw(t, task.Path); strings.Contains(raw, "done:") {
		t.Fatalf("new task was born with a done key:\n%s", raw)
	}

	if err := service.MarkDone(task.ID, true); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if !readFrontmatter(t, task.Path).Done {
		t.Fatalf("YAML does not say the task is done:\n%s", readRaw(t, task.Path))
	}
	if body, err := service.readNoteBody(task.Path); err != nil || body != "body" {
		t.Fatalf("body = %q err=%v, want %q", body, err, "body")
	}

	// Un-ticking removes the key again rather than leaving `done: false` behind.
	if err := service.MarkDone(task.ID, false); err != nil {
		t.Fatalf("mark undone: %v", err)
	}
	if raw := readRaw(t, task.Path); strings.Contains(raw, "done:") {
		t.Fatalf("un-ticked task kept a done key:\n%s", raw)
	}
}

// `done:` means nothing for a plain note, so the row's always-false Done must
// not strip a key the user wrote in Obsidian the next time Athena rewrites the
// file for an unrelated reason.
func TestRenameNoteKeepsHandWrittenDone(t *testing.T) {
	service, _ := newTestService(t)

	note, created, err := service.CreateNote(t.Context(), "Recipe", "body", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	if err := utils.OverwriteNoteFile(note.Path, "---\ntitle: Recipe\nkind: note\ndone: true\n---\n\nbody\n"); err != nil {
		t.Fatalf("hand-edit note: %v", err)
	}

	renamed, err := service.RenameNote(note.ID, "Recipe v2")
	if err != nil {
		t.Fatalf("rename note: %v", err)
	}
	if !readFrontmatter(t, renamed.Path).Done {
		t.Fatalf("rename dropped the user's done key:\n%s", readRaw(t, renamed.Path))
	}
}

// V-06: duplicating rebuilt the frontmatter from the row, which knows nothing
// about authors or ISBN — the copy came out as a plain note wearing a book's
// title, in the books folder, with its catalog metadata silently dropped.
func TestDuplicateBookKeepsBookFrontmatter(t *testing.T) {
	service, _ := newTestService(t)

	metadata := models.BookMetadata{
		Title: "Foundation", Authors: []string{"Isaac Asimov"},
		Genres: []string{"science fiction"}, PublishedYear: 1951,
		ISBN: "9780553293357", Source: "openlibrary",
	}
	book, created, err := service.CreateBook(t.Context(), metadata, "", time.Now())
	if err != nil || !created {
		t.Fatalf("create book: created=%t err=%v", created, err)
	}

	duplicate, err := service.DuplicateNote(t.Context(), book.ID, "Foundation Reread", "")
	if err != nil {
		t.Fatalf("duplicate book: %v", err)
	}

	frontmatter := readFrontmatter(t, duplicate.Path)
	if frontmatter.Kind != string(models.NoteTypeBook) {
		t.Fatalf("duplicate YAML kind = %q, want book", frontmatter.Kind)
	}
	if frontmatter.Title != "Foundation Reread" {
		t.Fatalf("duplicate YAML title = %q", frontmatter.Title)
	}
	if len(frontmatter.Authors) != 1 || frontmatter.Authors[0] != "Isaac Asimov" {
		t.Fatalf("duplicate lost its authors: %v", frontmatter.Authors)
	}
	if frontmatter.ISBN != metadata.ISBN || frontmatter.PublishedYear != metadata.PublishedYear {
		t.Fatalf("duplicate lost catalog data: isbn=%q year=%d", frontmatter.ISBN, frontmatter.PublishedYear)
	}
	if duplicate.Type != models.NoteTypeBook {
		t.Fatalf("duplicate row type = %q, want book", duplicate.Type)
	}
}
