package retrieval

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/utils"
)

func TestFolderInventoryReportsExistingRelationships(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	for _, folder := range []string{"work/Rumera", "ideas"} {
		if err := utils.EnsureDir(vault, folder); err != nil {
			t.Fatalf("create folder %q: %v", folder, err)
		}
	}
	workIndex, err := parser.RenderMarkdown(parser.Frontmatter{
		Title:         "Work",
		AthenaIndex:   true,
		LinkedFolders: []string{"ideas"},
	}, "")
	if err != nil {
		t.Fatalf("render folder index: %v", err)
	}
	if err := utils.WriteNoteFile(filepath.Join(vault, "work.md"), workIndex); err != nil {
		t.Fatalf("write folder index: %v", err)
	}

	service := NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), nil)
	entries, err := service.FolderInventory()
	if err != nil {
		t.Fatalf("folder inventory: %v", err)
	}
	work := findFolderEntry(entries, "work")
	if work == nil || work.Parent != "" || len(work.Children) != 1 || work.Children[0] != "work/Rumera" || len(work.LinkedFolders) != 1 || work.LinkedFolders[0] != "ideas" {
		t.Fatalf("work entry = %+v", work)
	}
	rumera := findFolderEntry(entries, "work/Rumera")
	if rumera == nil || rumera.Parent != "work" {
		t.Fatalf("Rumera entry = %+v", rumera)
	}
}

func TestNoteViewNeverExposesAbsoluteStoragePath(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	store := storage.NewNoteStore(db)
	path := filepath.Join(vault, "ideas", "future.md")
	if _, err := store.Create(&models.Note{Title: "Future", Path: path, Content: "A thought"}); err != nil {
		t.Fatalf("store note: %v", err)
	}

	service := NewService(vault, store, storage.NewChunkStore(db), nil)
	view, err := service.NoteByID(1)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if view == nil || view.Path != "ideas/future.md" {
		t.Fatalf("note view = %+v, want vault-relative path", view)
	}
}

func TestCatalogContextIsFullForChangesAndCompactForOrdinaryQuestions(t *testing.T) {
	catalog := []CatalogEntry{{ID: 1, Title: "Plan", Folder: "work", Rel: "work/plan.md"}}
	if !includeFullCatalog("move the Plan note to archive") {
		t.Fatal("change request did not request full catalog")
	}
	if includeFullCatalog("what did I write about launch risk?") {
		t.Fatal("content question requested distracting full catalog")
	}
	if got := formatCatalogSummary(catalog); got == "" || !strings.Contains(got, "1 active notes") {
		t.Fatalf("summary = %q", got)
	}
}

func findFolderEntry(entries []FolderEntry, path string) *FolderEntry {
	for index := range entries {
		if entries[index].Path == path {
			return &entries[index]
		}
	}
	return nil
}
