package storage

import (
	"math"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tiredbooy/internal/models"
)

// Embeddings are packed into a SQLite BLOB by hand because SQLite has no
// vector type. A byte-order or length slip there would not fail loudly; it
// would quietly corrupt every stored vector and degrade search forever.
func TestEmbeddingBlobRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		vec  []float32
	}{
		{"empty", []float32{}},
		{"negative and fractional", []float32{-1.5, 0.25, -0.0009765625, 3.5}},
		{"float32 extremes", []float32{math.MaxFloat32, -math.MaxFloat32, math.SmallestNonzeroFloat32}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blob := encodeEmbedding(tc.vec)
			if len(blob) != 4*len(tc.vec) {
				t.Fatalf("blob is %d bytes, want %d (4 bytes per float32)", len(blob), 4*len(tc.vec))
			}
			if got := decodeEmbedding(blob); !slices.Equal(got, tc.vec) {
				t.Fatalf("round trip = %v, want %v", got, tc.vec)
			}
		})
	}
}

func TestReplaceAllSwapsWholeIndex(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	notes := NewNoteStore(db)
	chunks := NewChunkStore(db)

	noteID, err := notes.Create(&models.Note{Title: "Note", Path: "/vault/note.md", Content: "body", Type: models.NoteTypeNote})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if _, err := chunks.Create(&models.Chunk{NoteID: noteID, Content: "stale", Embedding: []float32{1, 0}}); err != nil {
		t.Fatalf("create stale chunk: %v", err)
	}

	fresh := []*models.Chunk{
		{NoteID: noteID, Content: "fresh a", ChunkIdx: 0, Embedding: []float32{0.5, -0.5}},
		{NoteID: noteID, Content: "fresh b", ChunkIdx: 1, Embedding: []float32{0, 1}},
	}
	if err := chunks.ReplaceAll(fresh); err != nil {
		t.Fatalf("replace all: %v", err)
	}

	got, err := chunks.All()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if len(got) != len(fresh) {
		t.Fatalf("index holds %d chunks, want %d (old chunks were not replaced)", len(got), len(fresh))
	}
	// Compare by content rather than by row order: the swap is what matters,
	// not the ids SQLite happens to hand out.
	stored := map[string][]float32{}
	for _, c := range got {
		stored[c.Content] = c.Embedding
	}
	for _, want := range fresh {
		vec, ok := stored[want.Content]
		if !ok {
			t.Fatalf("chunk %q missing from index after replace", want.Content)
		}
		if !slices.Equal(vec, want.Embedding) {
			t.Fatalf("chunk %q embedding = %v, want %v", want.Content, vec, want.Embedding)
		}
	}
}

// ReplaceAll clears the index before refilling it, so a reindex that dies
// partway would leave the vault unsearchable if the transaction did not roll
// back. A failed reindex must never destroy working search.
func TestReplaceAllKeepsOldIndexWhenAnInsertFails(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	notes := NewNoteStore(db)
	chunks := NewChunkStore(db)

	noteID, err := notes.Create(&models.Note{Title: "Note", Path: "/vault/note.md", Content: "body", Type: models.NoteTypeNote})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if _, err := chunks.Create(&models.Chunk{NoteID: noteID, Content: "original", Embedding: []float32{1, 0}}); err != nil {
		t.Fatalf("create original chunk: %v", err)
	}

	// The second chunk points at a note that does not exist, so the chunks
	// note_id foreign key rejects it after the first insert already landed.
	const missingNoteID = 424242
	err = chunks.ReplaceAll([]*models.Chunk{
		{NoteID: noteID, Content: "rebuilt", Embedding: []float32{0, 1}},
		{NoteID: missingNoteID, Content: "orphan", Embedding: []float32{0, 1}},
	})
	if err == nil {
		t.Fatal("replace all succeeded with an orphan chunk; the rollback path never ran")
	}

	results, err := chunks.SearchSimilar([]float32{1, 0}, 10)
	if err != nil {
		t.Fatalf("search after failed reindex: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("search returned %d chunks after failed reindex, want the 1 original", len(results))
	}
	if results[0].Content != "original" {
		t.Fatalf("surviving chunk = %q, want %q", results[0].Content, "original")
	}
	if !slices.Equal(results[0].Embedding, []float32{1, 0}) {
		t.Fatalf("surviving embedding = %v, want [1 0]", results[0].Embedding)
	}
}
