package retrieval

import (
	"context"
	"fmt"
	"strings"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/utils"
)

// SearchNotes returns one best matching excerpt per note. It is the read-model
// counterpart to BuildContext: agent tools need structured facts, not a prompt
// fragment formatted for a human-like response.
func (s *Service) SearchNotes(ctx context.Context, query string, limit int) ([]RetrievedNote, error) {
	results, err := s.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	seen := make(map[int64]bool, len(results))
	out := make([]RetrievedNote, 0, len(results))
	for _, result := range results {
		if result.Score < MinSimilarity || seen[result.NoteID] {
			continue
		}
		note, err := s.noteStore.GetByID(result.NoteID)
		if err != nil {
			return nil, fmt.Errorf("load search result note: %w", err)
		}
		if note == nil {
			continue
		}
		seen[note.ID] = true
		out = append(out, RetrievedNote{
			ID:         note.ID,
			Title:      note.Title,
			Path:       utils.RelVault(s.vaultPath, note.Path),
			Similarity: result.Score,
			Content:    result.Content,
		})
	}
	return out, nil
}

func (s *Service) NoteByID(noteID int64) (*NoteView, error) {
	note, err := s.liveNoteByID(noteID)
	if err != nil || note == nil {
		return nil, err
	}
	return s.noteView(note), nil
}

// liveNoteByID is the trash guard for reading one note by ID.
//
// storage.NoteStore.GetByID has no trashed filter on purpose: trash is a soft
// delete, and internal/notes must still load a trashed row to restore it. The
// filter therefore belongs on the paths that feed model context. The catalog
// (NoteStore.AllMeta) and semantic search (ChunkStore.Searchable) already
// exclude trashed notes; this is the direct-by-ID path, which stays reachable
// in the very run that trashed the note because the model is still holding the
// old note_id.
//
// A trashed note is an error, not a nil miss: nil reads as "try another ID"
// and the model retries, while the error teaches it the note is gone. A
// genuinely unknown ID stays a nil miss so callers can report "not found".
func (s *Service) liveNoteByID(noteID int64) (*models.Note, error) {
	note, err := s.noteStore.GetByID(noteID)
	if err != nil || note == nil {
		return nil, err
	}
	if note.TrashedFrom != "" {
		// No title and no body in the message: the point is to stop trashed
		// content reaching the model, and an error string reaches it too.
		return nil, fmt.Errorf("note %d is in the trash and cannot be read; restore it first", noteID)
	}
	return note, nil
}

func (s *Service) Folders() ([]string, error) {
	return utils.ListFolders(s.vaultPath)
}

func (s *Service) FindNotesByTitle(query string, limit int) ([]CatalogEntry, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, fmt.Errorf("title query is required")
	}
	catalog, err := s.Inventory()
	if err != nil {
		return nil, err
	}
	matches := make([]CatalogEntry, 0, limit)
	for _, entry := range catalog {
		if strings.Contains(strings.ToLower(entry.Title), query) {
			matches = append(matches, entry)
			if len(matches) == limit {
				break
			}
		}
	}
	return matches, nil
}
