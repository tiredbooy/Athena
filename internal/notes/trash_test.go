package notes

import (
	"testing"

	"github.com/tiredbooy/internal/models"
)

func countFor(chunks []*models.Chunk, noteID int64) int {
	total := 0
	for _, chunk := range chunks {
		if chunk.NoteID == noteID {
			total++
		}
	}
	return total
}

// V-02: Athena keeps a trashed note's vectors rather than deleting them, so
// restore is instant and works with no embedding provider reachable. That only
// holds if nothing quietly strips them in the meantime — a reindex used to,
// and RestoreNote never re-embeds, so the note came back invisible to search.
// The other half of the same decision: while the note is trashed those kept
// vectors must stay out of every search path.
func TestTrashedNoteKeepsVectorsOutOfSearchAndSurvivesReindex(t *testing.T) {
	service, _ := newTestService(t)

	note, created, err := service.CreateNote(t.Context(), "Cold Storage", "a body worth keeping", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	if _, err := service.TrashNote(note.ID); err != nil {
		t.Fatalf("trash note: %v", err)
	}
	if err := service.Reindex(t.Context(), nil); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	searchable, err := service.chunkStore.Searchable()
	if err != nil {
		t.Fatalf("read searchable chunks: %v", err)
	}
	if got := countFor(searchable, note.ID); got != 0 {
		t.Fatalf("trashed note reached search with %d chunks", got)
	}

	if _, err := service.RestoreNote(note.ID); err != nil {
		t.Fatalf("restore note: %v", err)
	}
	searchable, err = service.chunkStore.Searchable()
	if err != nil {
		t.Fatalf("read searchable chunks after restore: %v", err)
	}
	if countFor(searchable, note.ID) == 0 {
		t.Fatal("restored note has no vectors; it is silently invisible to search")
	}
}
