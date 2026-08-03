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

func TestLinkFoldersCreatesPersistentBidirectionalObsidianLinks(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), nil)

	if err := utils.EnsureDir(vault, "work"); err != nil {
		t.Fatalf("create work folder: %v", err)
	}
	if err := utils.EnsureDir(vault, "hospital"); err != nil {
		t.Fatalf("create hospital folder: %v", err)
	}
	if _, err := service.LinkFolders([]string{"work", "hospital"}); err != nil {
		t.Fatalf("link folders: %v", err)
	}

	work, err := utils.ReadNoteFile(filepath.Join(vault, "work.md"))
	if err != nil || !strings.Contains(work, "[[hospital|Hospital]]") {
		t.Fatalf("work index = %q, err=%v", work, err)
	}
	hospital, err := utils.ReadNoteFile(filepath.Join(vault, "hospital.md"))
	if err != nil || !strings.Contains(hospital, "[[work|Work]]") {
		t.Fatalf("hospital index = %q, err=%v", hospital, err)
	}

	if err := service.SyncFolderGraph(); err != nil {
		t.Fatalf("resync graph: %v", err)
	}
	work, err = utils.ReadNoteFile(filepath.Join(vault, "work.md"))
	if err != nil || !strings.Contains(work, "[[hospital|Hospital]]") {
		t.Fatalf("work link was not preserved after graph sync: %q, err=%v", work, err)
	}
}
