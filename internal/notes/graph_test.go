package notes

import (
	"os"
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
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755); err != nil {
		t.Fatalf("create Obsidian settings directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, ".obsidian", "graph.json"), []byte(`{"search":"tag:important","colorGroups":[{"query":"tag:important","color":"rgba(1, 2, 3, 1)"}]}`), 0o644); err != nil {
		t.Fatalf("write existing graph settings: %v", err)
	}

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
	graph, err := utils.ReadNoteFile(filepath.Join(vault, ".obsidian", "graph.json"))
	if err != nil || !strings.Contains(graph, `"search": "tag:important"`) || !strings.Contains(graph, `"query": "tag:important"`) || !strings.Contains(graph, `"query": "path:library.md"`) || !strings.Contains(graph, `"rgb":`) {
		t.Fatalf("graph settings = %q, err=%v", graph, err)
	}
}

func TestAddFolderGraphColorsIncludesOnlyDirectChildren(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), nil)
	for _, folder := range []string{"books/reading", "books/finished", "books/reading/technology", "work"} {
		if err := utils.EnsureDir(vault, folder); err != nil {
			t.Fatalf("create %s: %v", folder, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, ".obsidian", "graph.json"), []byte(`{"colorGroups":[{"query":"tag:important","color":{"a":1,"rgb":66051}},{"query":"path:books.md","color":"rgba(1, 2, 3, 1)"}]}`), 0o644); err != nil {
		t.Fatalf("write graph settings: %v", err)
	}

	colored, err := service.AddFolderGraphColors("books", true)
	if err != nil {
		t.Fatalf("add folder graph colors: %v", err)
	}
	if got, want := strings.Join(colored, ","), "books,books/finished,books/reading"; got != want {
		t.Fatalf("colored folders = %q, want %q", got, want)
	}
	graph, err := utils.ReadNoteFile(filepath.Join(vault, ".obsidian", "graph.json"))
	if err != nil {
		t.Fatalf("read graph settings: %v", err)
	}
	for _, query := range []string{`"query": "tag:important"`, `"query": "path:books.md"`, `"query": "path:books/finished.md"`, `"query": "path:books/reading.md"`} {
		if !strings.Contains(graph, query) {
			t.Fatalf("graph settings missing %s: %s", query, graph)
		}
	}
	// Top-level folders receive the app's baseline colors during graph sync;
	// this action must not expand beyond books' direct children into descendants.
	for _, query := range []string{"path:books/reading/technology.md"} {
		if strings.Contains(graph, query) {
			t.Fatalf("graph settings unexpectedly contains %s: %s", query, graph)
		}
	}
	if !strings.Contains(graph, `"a": 1`) || !strings.Contains(graph, `"rgb":`) {
		t.Fatalf("graph color uses an invalid Obsidian format: %s", graph)
	}
	if strings.Contains(graph, `"color": "rgba(`) {
		t.Fatalf("graph color did not repair the obsolete CSS format: %s", graph)
	}
}

func TestSetGraphNodeSizeMultiplierPreservesOtherGraphSettings(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), nil)
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, ".obsidian", "graph.json"), []byte(`{"showTags":false,"colorGroups":[{"query":"tag:important","color":{"a":1,"rgb":66051}}]}`), 0o644); err != nil {
		t.Fatalf("write graph settings: %v", err)
	}
	if err := service.SetGraphNodeSizeMultiplier(1.4); err != nil {
		t.Fatalf("set graph node size: %v", err)
	}
	if err := service.VerifyGraphNodeSizeMultiplier(1.4); err != nil {
		t.Fatalf("verify graph node size: %v", err)
	}
	graph, err := utils.ReadNoteFile(filepath.Join(vault, ".obsidian", "graph.json"))
	if err != nil || !strings.Contains(graph, `"nodeSizeMultiplier": 1.4`) || !strings.Contains(graph, `"query": "tag:important"`) {
		t.Fatalf("graph settings = %q, err=%v", graph, err)
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

	if _, err := service.UnlinkFolders([]string{"work", "hospital"}); err != nil {
		t.Fatalf("unlink folders: %v", err)
	}
	work, err = utils.ReadNoteFile(filepath.Join(vault, "work.md"))
	if err != nil || strings.Contains(work, "[[hospital|Hospital]]") {
		t.Fatalf("work link was not removed: %q, err=%v", work, err)
	}
	hospital, err = utils.ReadNoteFile(filepath.Join(vault, "hospital.md"))
	if err != nil || strings.Contains(hospital, "[[work|Work]]") {
		t.Fatalf("hospital link was not removed: %q, err=%v", hospital, err)
	}
}
