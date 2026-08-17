package storage

import (
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/models"
)

// Scoring guards: vectors of different lengths mean the embedding model
// changed under a stale index, and a zero vector means the model returned
// nothing usable. Both score 0 so the chunk sinks in the ranking instead of
// producing a garbage similarity (or an index-out-of-range panic).
func TestCosineSimilarityScoresAndGuards(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"same direction", []float32{1, 0}, []float32{2, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"dimension mismatch", []float32{1, 0, 0}, []float32{1, 0}, 0},
		{"both empty", nil, nil, 0},
		{"zero query", []float32{0, 0}, []float32{1, 0}, 0},
		{"zero chunk", []float32{1, 0}, []float32{0, 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cosineSimilarity(tc.a, tc.b); got != tc.want {
				t.Fatalf("cosineSimilarity(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// Trashing a note leaves its vectors in the chunks table, so search is the
// only thing standing between soft-deleted content and the model prompt.
func TestSearchSimilarExcludesTrashedNotes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	notes := NewNoteStore(db)
	chunks := NewChunkStore(db)

	liveID, err := notes.Create(&models.Note{Title: "Live", Path: "/vault/live.md", Content: "live", Type: models.NoteTypeNote})
	if err != nil {
		t.Fatalf("create live note: %v", err)
	}
	trashedID, err := notes.Create(&models.Note{Title: "Trashed", Path: "/vault/.trash/gone.md", Content: "gone", Type: models.NoteTypeNote, TrashedFrom: "gone.md"})
	if err != nil {
		t.Fatalf("create trashed note: %v", err)
	}

	// The trashed chunk is the better match, so a missing filter cannot pass
	// this test by luck of ordering.
	query := []float32{1, 0}
	for _, c := range []*models.Chunk{
		{NoteID: liveID, Content: "live chunk", Embedding: []float32{0.7, 0.7}},
		{NoteID: trashedID, Content: "trashed chunk", Embedding: []float32{1, 0}},
	} {
		if _, err := chunks.Create(c); err != nil {
			t.Fatalf("create chunk: %v", err)
		}
	}

	results, err := chunks.SearchSimilar(query, 10)
	if err != nil {
		t.Fatalf("search similar: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want only the live note's chunk", len(results))
	}
	if results[0].NoteID != liveID {
		t.Fatalf("result note_id = %d, want %d (trashed note leaked into search)", results[0].NoteID, liveID)
	}
}
