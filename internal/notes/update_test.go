package notes

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/utils"
)

func TestReplaceMarkdownSectionPreservesOtherSections(t *testing.T) {
	body := "# Summary\nOld summary\n\n## Detail\nKeep this\n\n# Next\nUnchanged"
	updated, current, found := replaceMarkdownSection(body, "Summary", "New summary")
	if !found || current != "Old summary\n\n## Detail\nKeep this" {
		t.Fatalf("found=%t current=%q", found, current)
	}
	want := "# Summary\nNew summary\n# Next\nUnchanged"
	if updated != want {
		t.Fatalf("updated=%q, want %q", updated, want)
	}
}

func TestReplaceMarkdownSectionRejectsMissingSection(t *testing.T) {
	_, _, found := replaceMarkdownSection("# Present\nText", "Missing", "Replacement")
	if found {
		t.Fatal("found missing section")
	}
}

func TestMoveNoteRepairsPathAfterManualFolderMove(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	store := storage.NewNoteStore(db)
	service := NewService(vault, store, storage.NewChunkStore(db), nil)

	oldPath := filepath.Join(vault, "book", "finished", "animal-farm.md")
	newPath := filepath.Join(vault, "books", "finished", "animal-farm.md")
	if err := utils.WriteNoteFile(newPath, "---\ntitle: Animal Farm\n---\n\nA novel."); err != nil {
		t.Fatalf("write manually moved note: %v", err)
	}
	if err := utils.EnsureDir(vault, "books/finished/classics"); err != nil {
		t.Fatalf("create destination folder: %v", err)
	}
	note := &models.Note{Title: "Animal Farm", Path: oldPath, Content: "A novel."}
	id, err := store.Create(note)
	if err != nil {
		t.Fatalf("store stale note: %v", err)
	}

	moved, err := service.MoveNote(context.Background(), id, "books/finished/classics")
	if err != nil {
		t.Fatalf("move reconciled note: %v", err)
	}
	wantPath := filepath.Join(vault, "books", "finished", "classics", "animal-farm.md")
	if moved.Path != wantPath {
		t.Fatalf("path = %s, want %s", moved.Path, wantPath)
	}
	if exists, err := utils.ReadNoteFile(wantPath); err != nil || exists == "" {
		t.Fatalf("moved file missing: %v", err)
	}
}

func TestRenameNoteRepairsMissingStoredPathBeforeRenaming(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	store := storage.NewNoteStore(db)
	service := NewService(vault, store, storage.NewChunkStore(db), nil)

	actualPath := filepath.Join(vault, "book", "reading", "reading-designing-data-intensive-applications-d3.md")
	if err := utils.WriteNoteFile(actualPath, "---\ntitle: Reading Designing Data-Intensive Applications\n---\n\nA reading note."); err != nil {
		t.Fatalf("write note: %v", err)
	}
	stalePath := filepath.Join(vault, "book", "reading", "designing-data-intensive-applications.md", "reading-designing-data-intensive-applications-d3.md")
	note := &models.Note{Title: "Reading Designing Data-Intensive Applications", Path: stalePath, Content: "A reading note."}
	id, err := store.Create(note)
	if err != nil {
		t.Fatalf("store stale note: %v", err)
	}

	renamed, err := service.RenameNote(id, "Designing Data-Intensive Applications")
	if err != nil {
		t.Fatalf("rename reconciled note: %v", err)
	}
	wantPath := filepath.Join(vault, "book", "reading", "designing-data-intensive-applications.md")
	if renamed.Path != wantPath {
		t.Fatalf("path = %s, want %s", renamed.Path, wantPath)
	}
	if _, err := utils.ReadNoteFile(wantPath); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
}
