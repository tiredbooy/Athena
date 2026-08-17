package notes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/utils"
)

// S-04: archive and trash both move a note away and record where it came from,
// so stacking them nests the paths (.trash/archive/...) and the recorded origin
// of the second move is itself a relocation — restore would put the note
// somewhere it never lived.
func TestArchiveAndTrashCannotStack(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	service, vault := newTestService(t)

	trashed, created, err := service.CreateNote(t.Context(), "Trashed First", "body", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	if _, err := service.TrashNote(trashed.ID); err != nil {
		t.Fatalf("trash note: %v", err)
	}
	if _, err := service.ArchiveNote(trashed.ID); err == nil {
		t.Fatal("archiving a trashed note was accepted; its path and origin are now nested")
	}
	stored, err := service.noteStore.GetByID(trashed.ID)
	if err != nil {
		t.Fatalf("reload trashed note: %v", err)
	}
	if stored.Archived || stored.ArchivedFrom != "" {
		t.Fatalf("refused archive still marked the note: archived=%t from=%q", stored.Archived, stored.ArchivedFrom)
	}
	if _, err := os.Stat(stored.Path); err != nil {
		t.Fatalf("trashed note is no longer at %s: %v", stored.Path, err)
	}
	if _, err := os.Stat(filepath.Join(vault, "archive")); !os.IsNotExist(err) {
		t.Fatalf("refused archive created the archive tree (stat: %v)", err)
	}

	archived, created, err := service.CreateNote(t.Context(), "Archived First", "body", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	if _, err := service.ArchiveNote(archived.ID); err != nil {
		t.Fatalf("archive note: %v", err)
	}
	if _, err := service.TrashNote(archived.ID); err == nil {
		t.Fatal("trashing an archived note was accepted; its path and origin are now nested")
	}
	stored, err = service.noteStore.GetByID(archived.ID)
	if err != nil {
		t.Fatalf("reload archived note: %v", err)
	}
	if stored.TrashedFrom != "" {
		t.Fatalf("refused trash still marked the note as trashed from %q", stored.TrashedFrom)
	}
	if _, err := os.Stat(stored.Path); err != nil {
		t.Fatalf("archived note is no longer at %s: %v", stored.Path, err)
	}
}

// S-07: a Markdown file can sit in the vault with no row in SQLite — Obsidian
// wrote it, or a partial write left it. A bare os.Rename in MoveNote would
// overwrite it and destroy content Athena never indexed.
func TestMoveNoteRefusesDestinationFileWithoutRow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	service, vault := newTestService(t)

	note, created, err := service.CreateNote(t.Context(), "Stray Target", "athena body", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	if _, err := service.CreateFolder("refs"); err != nil {
		t.Fatalf("create folder: %v", err)
	}

	dest, err := utils.NotePath(vault, "refs", note.Title)
	if err != nil {
		t.Fatalf("build destination path: %v", err)
	}
	const strayBody = "written in Obsidian, unknown to Athena"
	if err := os.WriteFile(dest, []byte(strayBody), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	if _, err := service.MoveNote(t.Context(), note.ID, "refs"); err == nil {
		t.Fatal("move onto an unindexed file was accepted")
	}
	survived, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read stray file after refused move: %v", err)
	}
	if string(survived) != strayBody {
		t.Fatalf("refused move clobbered the unindexed file at %s", dest)
	}
	if _, err := os.Stat(note.Path); err != nil {
		t.Fatalf("source note left %s after a refused move: %v", note.Path, err)
	}
}
