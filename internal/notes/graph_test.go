package notes

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/utils"
)

func TestSyncFolderGraphBuildsParentCategoryAndItemLinks(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	store := storage.NewNoteStore(db)
	service := NewService(vault, store, storage.NewChunkStore(db), nil)

	if err := utils.EnsureDir(vault, "library/genre"); err != nil {
		t.Fatalf("create folders: %v", err)
	}
	notePath, err := utils.NotePath(vault, "library/genre", "Example Item")
	if err != nil {
		t.Fatalf("build note path: %v", err)
	}
	if err := utils.WriteNoteFile(notePath, "# Example Item\n"); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if _, err := store.Create(&models.Note{Title: "Example Item", Path: notePath, Content: ""}); err != nil {
		t.Fatalf("store note: %v", err)
	}
	if err := service.SyncFolderGraph(); err != nil {
		t.Fatalf("sync graph: %v", err)
	}

	parent, err := utils.ReadNoteFile(filepath.Join(vault, "library.md"))
	if err != nil || !strings.Contains(parent, "[[library/genre|Genre]]") {
		t.Fatalf("parent index = %q, err=%v", parent, err)
	}
	category, err := utils.ReadNoteFile(filepath.Join(vault, "library", "genre.md"))
	if err != nil || !strings.Contains(category, "[[library|Library]]") || !strings.Contains(category, "[[library/genre/example-item|Example Item]]") {
		t.Fatalf("category index = %q, err=%v", category, err)
	}
}
