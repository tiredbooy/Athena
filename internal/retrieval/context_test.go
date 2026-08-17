package retrieval

import (
	"context"
	"database/sql"
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

// The catalog is titles and ids, so it must not read note bodies out of
// SQLite. Dropping the content column is what makes that property observable
// at runtime instead of asserting on the text of a query: an inventory that
// still works without the column provably never read it.
func TestInventoryDoesNotLoadNoteBodies(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := storage.NewNoteStore(db)
	if _, err := store.Create(&models.Note{
		Title:   "Big",
		Path:    filepath.Join(vault, "big.md"),
		Content: strings.Repeat("body ", 200000),
	}); err != nil {
		t.Fatalf("store note: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE notes DROP COLUMN content`); err != nil {
		t.Fatalf("drop content column: %v", err)
	}

	catalog, err := NewService(vault, store, storage.NewChunkStore(db), nil).Inventory()
	if err != nil {
		t.Fatalf("inventory read note bodies: %v", err)
	}
	if len(catalog) != 1 || catalog[0].Title != "Big" || catalog[0].Rel != "big.md" {
		t.Fatalf("catalog = %+v", catalog)
	}
}

// Trashed notes must not reach the model through either half of the context:
// the inventory the model reads for "what notes do I have", or the semantic
// hits appended as "relevant memories". Here the trashed note's vector is the
// *better* match, so a missing filter cannot pass by luck.
func TestBuildContextExcludesTrashedNotes(t *testing.T) {
	vault := t.TempDir()
	db := openTestDB(t)
	notes := storage.NewNoteStore(db)
	chunks := storage.NewChunkStore(db)

	liveID, err := notes.Create(&models.Note{Title: "Launch plan", Path: filepath.Join(vault, "launch.md"), Content: "ship it"})
	if err != nil {
		t.Fatalf("create live note: %v", err)
	}
	trashedID, err := notes.Create(&models.Note{Title: "Old launch plan", Path: filepath.Join(vault, ".trash", "old.md"), Content: "abandoned", TrashedFrom: "old.md"})
	if err != nil {
		t.Fatalf("create trashed note: %v", err)
	}
	for _, chunk := range []*models.Chunk{
		{NoteID: liveID, Content: "ship it", Embedding: []float32{0.9, 0.1}},
		{NoteID: trashedID, Content: "abandoned", Embedding: []float32{1, 0}},
	} {
		if _, err := chunks.Create(chunk); err != nil {
			t.Fatalf("store chunk: %v", err)
		}
	}

	service := NewService(vault, notes, chunks, &countingEmbedder{vector: []float32{1, 0}})
	// "what notes" asks for the full inventory, so this one query exercises
	// both the catalog listing and the semantic hits.
	result, err := service.BuildContext(context.Background(), "what notes do I have about the launch?", 4)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(result.Catalog) != 1 || result.Catalog[0].ID != liveID {
		t.Fatalf("catalog = %+v, want only the live note", result.Catalog)
	}
	if len(result.Results) != 1 || result.Results[0].ID != liveID {
		t.Fatalf("results = %+v, want only the live note", result.Results)
	}
	if strings.Contains(result.Context, "Old launch plan") || strings.Contains(result.Context, "abandoned") {
		t.Fatalf("trashed note leaked into prompt context:\n%s", result.Context)
	}

	inventory, err := service.Inventory()
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if len(inventory) != 1 || inventory[0].ID != liveID {
		t.Fatalf("inventory = %+v, want only the live note", inventory)
	}
}

// Vector search always returns its topK nearest chunks, however far away they
// are. MinSimilarity is what stops an unrelated note from being presented to
// the model as a relevant memory.
func TestBuildContextDropsHitsBelowMinSimilarity(t *testing.T) {
	vault := t.TempDir()
	db := openTestDB(t)
	notes := storage.NewNoteStore(db)
	chunks := storage.NewChunkStore(db)

	noteID, err := notes.Create(&models.Note{Title: "Sourdough", Path: filepath.Join(vault, "sourdough.md"), Content: "feed the starter"})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	// Orthogonal to the query: cosine 0, the nearest chunk there is and still
	// nowhere near relevant.
	if _, err := chunks.Create(&models.Chunk{NoteID: noteID, Content: "feed the starter", Embedding: []float32{0, 1}}); err != nil {
		t.Fatalf("store chunk: %v", err)
	}

	service := NewService(vault, notes, chunks, &countingEmbedder{vector: []float32{1, 0}})
	result, err := service.BuildContext(context.Background(), "how do I renew my passport?", 4)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(result.Results) != 0 {
		t.Fatalf("results = %+v, want the weak hit dropped", result.Results)
	}
	if !strings.Contains(result.Context, "No related notes found") {
		t.Fatalf("context did not report the empty result set:\n%s", result.Context)
	}
	if strings.Contains(result.Context, "feed the starter") {
		t.Fatalf("unrelated note stuffed into prompt context:\n%s", result.Context)
	}
}

// An empty vault has nothing to rank, so embedding the question can only add
// latency (and, with a cold local model, seconds of it) to an answer that is
// already decided.
func TestBuildContextSkipsEmbeddingForEmptyVault(t *testing.T) {
	vault := t.TempDir()
	db := openTestDB(t)
	embedder := &countingEmbedder{vector: []float32{1, 0}}

	service := NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), embedder)
	result, err := service.BuildContext(context.Background(), "what did I write about launch risk?", 4)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if embedder.calls != 0 {
		t.Fatalf("embedded the query %d times for an empty vault, want 0", embedder.calls)
	}
	if len(result.Results) != 0 {
		t.Fatalf("results = %+v, want none", result.Results)
	}
	if !strings.Contains(result.Context, "empty") {
		t.Fatalf("context did not state the vault is empty:\n%s", result.Context)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// countingEmbedder is a stub embedding provider: it returns a fixed vector
// with no network call and records how often it was asked, which is how the
// empty-vault test observes that the round trip was skipped.
type countingEmbedder struct {
	vector []float32
	calls  int
}

func (e *countingEmbedder) Name() string       { return "stub" }
func (e *countingEmbedder) EmbedModel() string { return "stub-embed" }
func (e *countingEmbedder) Embed(context.Context, string) ([]float32, error) {
	e.calls++
	return e.vector, nil
}

func findFolderEntry(entries []FolderEntry, path string) *FolderEntry {
	for index := range entries {
		if entries[index].Path == path {
			return &entries[index]
		}
	}
	return nil
}
