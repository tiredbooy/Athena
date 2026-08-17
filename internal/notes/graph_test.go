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

	colored, err := service.AddFolderGraphColors("books", true, "")
	if err != nil {
		t.Fatalf("add folder graph colors: %v", err)
	}
	names := make([]string, 0, len(colored))
	for _, style := range colored {
		if style.Color == "" {
			t.Fatalf("folder %q was styled without reporting a color", style.Folder)
		}
		names = append(names, style.Folder)
	}
	if got, want := strings.Join(names, ","), "books,books/finished,books/reading"; got != want {
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

func graphService(t *testing.T) (*Service, string) {
	t.Helper()
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), nil), vault
}

// G-02: "make the work orb blue" has to actually produce blue, and the user
// must be told which color landed.
func TestExplicitColorIsAppliedAndReported(t *testing.T) {
	service, vault := graphService(t)
	if err := utils.EnsureDir(vault, "work"); err != nil {
		t.Fatalf("create work: %v", err)
	}

	styles, err := service.AddFolderGraphColors("work", false, "#3498DB")
	if err != nil {
		t.Fatalf("set explicit color: %v", err)
	}
	if len(styles) != 1 || styles[0].Folder != "work" || styles[0].Color != "#3498DB" {
		t.Fatalf("styles = %+v, want work at #3498DB", styles)
	}

	graph, err := utils.ReadNoteFile(filepath.Join(vault, ".obsidian", "graph.json"))
	if err != nil {
		t.Fatalf("read graph settings: %v", err)
	}
	// 0x3498DB = 3447003.
	if !strings.Contains(graph, "3447003") {
		t.Fatalf("requested color was not written: %s", graph)
	}
}

func TestInvalidExplicitColorIsRejected(t *testing.T) {
	service, vault := graphService(t)
	if err := utils.EnsureDir(vault, "work"); err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := service.AddFolderGraphColors("work", false, "cornflower"); err == nil {
		t.Fatal("expected a non-hex color to be refused")
	}
}

// "Better" must mean visibly different from the neighbours, not a hash that can
// land next to a sibling's color.
func TestDefaultColorContrastsWithSiblings(t *testing.T) {
	service, vault := graphService(t)
	for _, folder := range []string{"work", "personal", "books"} {
		if err := utils.EnsureDir(vault, folder); err != nil {
			t.Fatalf("create %s: %v", folder, err)
		}
	}
	if err := service.SyncFolderGraph(); err != nil {
		t.Fatalf("sync graph: %v", err)
	}

	styles, err := service.AddFolderGraphColors("work", false, "")
	if err != nil {
		t.Fatalf("style work: %v", err)
	}
	if len(styles) != 1 {
		t.Fatalf("styles = %+v, want one entry", styles)
	}

	// Every sibling orb must be a distinct color, or the graph cannot be read.
	seen := make(map[string]string)
	for _, folder := range []string{"work", "personal", "books"} {
		got, err := service.AddFolderGraphColors(folder, false, "")
		if err != nil {
			t.Fatalf("style %s: %v", folder, err)
		}
		if owner, clash := seen[got[0].Color]; clash {
			t.Fatalf("%s reused %s already taken by %s", folder, got[0].Color, owner)
		}
		seen[got[0].Color] = folder
	}
}

// G-03: "add projects to the graph" means a node the user can see, not a bare
// directory — folder, Athena index note, and an orb color in one action.
func TestAddFolderToGraphCreatesFolderIndexAndColor(t *testing.T) {
	service, vault := graphService(t)
	if err := utils.EnsureDir(vault, "work"); err != nil {
		t.Fatalf("create work: %v", err)
	}

	style, err := service.AddFolderToGraph("work/projects", "")
	if err != nil {
		t.Fatalf("add folder to graph: %v", err)
	}
	if style.Folder != "work/projects" || style.Color == "" {
		t.Fatalf("style = %+v, want work/projects with a color", style)
	}

	exists, err := utils.FolderExists(vault, "work/projects")
	if err != nil || !exists {
		t.Fatalf("folder exists = %t, err=%v", exists, err)
	}
	index, err := utils.ReadNoteFile(filepath.Join(vault, "work", "projects.md"))
	if err != nil || !strings.Contains(index, "athena_index: true") {
		t.Fatalf("index note = %q, err=%v", index, err)
	}
	graph, err := utils.ReadNoteFile(filepath.Join(vault, ".obsidian", "graph.json"))
	if err != nil || !strings.Contains(graph, `"query": "path:work/projects.md"`) {
		t.Fatalf("graph settings = %q, err=%v", graph, err)
	}
	// The reported color is the one a verifier and the user will both see.
	if err := service.VerifyFolderInGraph("work/projects"); err != nil {
		t.Fatalf("verify folder in graph: %v", err)
	}
}

// A guessed parent must be named, not silently created: the whole point is that
// the vault never grows a tree nobody asked for.
func TestAddFolderToGraphRefusesMissingParent(t *testing.T) {
	service, vault := graphService(t)

	_, err := service.AddFolderToGraph("work/projects", "")
	if err == nil {
		t.Fatal("expected a missing parent to be refused")
	}
	if !strings.Contains(err.Error(), `"work"`) {
		t.Fatalf("error %q does not name the missing parent", err)
	}
	for _, folder := range []string{"work", "work/projects"} {
		exists, existsErr := utils.FolderExists(vault, folder)
		if existsErr != nil {
			t.Fatalf("check %s: %v", folder, existsErr)
		}
		if exists {
			t.Fatalf("%s was created despite the missing parent", folder)
		}
	}
}

// The contrast search must be a pure function of what is already on screen, so
// the same vault renders the same way on every machine.
func TestContrastingGraphColorIsDeterministic(t *testing.T) {
	used := []graphColor{{Alpha: 1, RGB: 0xE67E22}, {Alpha: 1, RGB: 0x3498DB}}
	first := contrastingGraphColor(used)
	for i := 0; i < 5; i++ {
		if got := contrastingGraphColor(used); got != first {
			t.Fatalf("run %d picked %s, want the stable %s", i, got.Hex(), first.Hex())
		}
	}
	for _, existing := range used {
		if first.RGB == existing.RGB {
			t.Fatalf("picked %s, which a sibling already uses", first.Hex())
		}
	}
	if got := contrastingGraphColor(nil); got.RGB != graphPalette[0] {
		t.Fatalf("with no siblings the first palette entry should win, got %s", got.Hex())
	}
}

// G-04 stays true: a color the user chose is never replaced by the default.
func TestUserChosenColorSurvivesDefaultStyling(t *testing.T) {
	service, vault := graphService(t)
	if err := utils.EnsureDir(vault, "work"); err != nil {
		t.Fatalf("create work: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	// 0x123456 = 1193046, a color Athena's palette would never choose.
	settings := `{"colorGroups":[{"query":"path:work.md","color":{"a":1,"rgb":1193046}}]}`
	if err := os.WriteFile(filepath.Join(vault, ".obsidian", "graph.json"), []byte(settings), 0o644); err != nil {
		t.Fatalf("write graph settings: %v", err)
	}

	styles, err := service.AddFolderGraphColors("work", false, "")
	if err != nil {
		t.Fatalf("style work: %v", err)
	}
	if styles[0].Color != "#123456" {
		t.Fatalf("reported %s, want the user's #123456 kept", styles[0].Color)
	}

	graph, err := utils.ReadNoteFile(filepath.Join(vault, ".obsidian", "graph.json"))
	if err != nil {
		t.Fatalf("read graph settings: %v", err)
	}
	if !strings.Contains(graph, "1193046") {
		t.Fatalf("user color was overwritten: %s", graph)
	}
}

// G-04, the two cases the single-folder test above cannot reach: a child that
// the user coloured must survive a default include_children pass over a parent
// that has no colour yet, and an explicit request must still win over that same
// user colour — "never overwrite" is about Athena's own guesses, not about the
// user asking for a colour.
func TestUserColorSurvivesIncludeChildrenButNotAnExplicitRequest(t *testing.T) {
	service, vault := graphService(t)
	for _, folder := range []string{"books/reading", "books/finished"} {
		if err := utils.EnsureDir(vault, folder); err != nil {
			t.Fatalf("create %s: %v", folder, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	// Only the child is coloured, and with 0x123456 — a value Athena's palette
	// never produces, so a match proves the user's entry was kept rather than
	// re-picked.
	settings := `{"colorGroups":[{"query":"path:books/reading.md","color":{"a":1,"rgb":1193046}}]}`
	if err := os.WriteFile(filepath.Join(vault, ".obsidian", "graph.json"), []byte(settings), 0o644); err != nil {
		t.Fatalf("write graph settings: %v", err)
	}

	styles, err := service.AddFolderGraphColors("books", true, "")
	if err != nil {
		t.Fatalf("style books tree: %v", err)
	}
	byFolder := make(map[string]string, len(styles))
	for _, style := range styles {
		byFolder[style.Folder] = style.Color
	}
	if got := byFolder["books/reading"]; got != "#123456" {
		t.Fatalf("books/reading = %q, want the user's #123456 kept", got)
	}
	if byFolder["books"] == "" || byFolder["books/finished"] == "" {
		t.Fatalf("Athena did not fill the missing groups: %+v", styles)
	}
	graph, err := utils.ReadNoteFile(filepath.Join(vault, ".obsidian", "graph.json"))
	if err != nil {
		t.Fatalf("read graph settings: %v", err)
	}
	if !strings.Contains(graph, "1193046") {
		t.Fatalf("user color was overwritten during include_children: %s", graph)
	}

	// An explicit request is the user choosing, so it replaces even that.
	styles, err = service.AddFolderGraphColors("books", true, "#3498DB")
	if err != nil {
		t.Fatalf("apply explicit color: %v", err)
	}
	for _, style := range styles {
		if style.Color != "#3498DB" {
			t.Fatalf("%s = %q, want the requested #3498DB", style.Folder, style.Color)
		}
	}
	graph, err = utils.ReadNoteFile(filepath.Join(vault, ".obsidian", "graph.json"))
	if err != nil {
		t.Fatalf("read graph settings: %v", err)
	}
	if strings.Contains(graph, "1193046") {
		t.Fatalf("explicit request did not replace the old color: %s", graph)
	}
}
