package notes

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/utils"
)

type bookTestEmbedder struct{}

func (bookTestEmbedder) Name() string       { return "book-test" }
func (bookTestEmbedder) EmbedModel() string { return "book-test" }

func (bookTestEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

func TestCreateBookPromotesExistingGenericNoteInPlace(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), bookTestEmbedder{})
	if err := utils.EnsureDir(vault, "books/reading"); err != nil {
		t.Fatalf("create reading folder: %v", err)
	}
	existing, created, err := service.CreateNote(t.Context(), "Thinking Fast And Slow", "My existing observations", "books/reading", []string{"psychology"})
	if err != nil || !created {
		t.Fatalf("create generic note: created=%t err=%v", created, err)
	}
	startedAt := time.Date(2026, 8, 13, 10, 30, 0, 0, time.FixedZone("IRST", 3*60*60+30*60))
	metadata := models.BookMetadata{
		Title: "Thinking, Fast and Slow", Authors: []string{"Daniel Kahneman"},
		Genres: []string{"Psychology"}, PublishedYear: 2011, Source: "open_library",
	}

	book, promoted, err := service.CreateBook(t.Context(), metadata, "books/reading", startedAt)
	if err != nil {
		t.Fatalf("promote book: %v", err)
	}
	if !promoted || book.ID != existing.ID || book.Type != models.NoteTypeBook {
		t.Fatalf("book=%+v promoted=%t existing=%+v", book, promoted, existing)
	}
	if book.Title != metadata.Title {
		t.Fatalf("title = %q, want catalog title %q", book.Title, metadata.Title)
	}
	raw, err := utils.ReadNoteFile(book.Path)
	if err != nil {
		t.Fatalf("read promoted book: %v", err)
	}
	fm, body, err := parser.ParseMarkdown(raw)
	if err != nil {
		t.Fatalf("parse promoted book: %v", err)
	}
	if fm.Kind != "book" || fm.MetadataSource != "open_library" || fm.StartedAt.IsZero() || !strings.Contains(strings.Join(fm.Tags, ","), "book") {
		t.Fatalf("frontmatter = %+v", fm)
	}
	if body != "My existing observations" {
		t.Fatalf("body = %q, want preserved observations", body)
	}
}

func TestUpdateBookMetadataPreservesBodyAndRecordsUserFallback(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), bookTestEmbedder{})
	if err := utils.EnsureDir(vault, "books/reading"); err != nil {
		t.Fatal(err)
	}
	metadata := models.BookMetadata{Title: "Offline Book", Source: "unresolved"}
	book, created, err := service.CreateBook(t.Context(), metadata, "books/reading", time.Now())
	if err != nil || !created {
		t.Fatalf("create book: created=%t err=%v", created, err)
	}
	if err := service.UpdateBookMetadata(t.Context(), book.ID, []string{"Known Author"}, []string{"Science Fiction"}); err != nil {
		t.Fatal(err)
	}
	fm, err := service.BookMetadata(book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fm.MetadataSource != "user" || len(fm.Authors) != 1 || fm.Authors[0] != "Known Author" || fm.Genres[0] != "Science Fiction" {
		t.Fatalf("frontmatter = %+v", fm)
	}
	raw, err := utils.ReadNoteFile(book.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := parser.ParseMarkdown(raw)
	if err != nil || body != "# Reading notes\n\n" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestUpdateBookMetadataDoesNotReplaceCatalogFacts(t *testing.T) {
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), bookTestEmbedder{})
	if err := utils.EnsureDir(vault, "books/reading"); err != nil {
		t.Fatal(err)
	}
	metadata := models.BookMetadata{Title: "Foundation", Authors: []string{"Isaac Asimov"}, Genres: []string{"Science Fiction"}, Source: "open_library"}
	book, _, err := service.CreateBook(t.Context(), metadata, "books/reading", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateBookMetadata(t.Context(), book.ID, []string{"Different Author"}, nil); err == nil || !strings.Contains(err.Error(), "cannot replace") {
		t.Fatalf("replacement error = %v", err)
	}
	fm, err := service.BookMetadata(book.ID)
	if err != nil || fm.Authors[0] != "Isaac Asimov" || fm.MetadataSource != "open_library" {
		t.Fatalf("frontmatter=%+v err=%v", fm, err)
	}
}
