package retrieval

import (
	"context"
	"fmt"
	"strings"

	"github.com/tiredbooy/internal/models"
)

// MinSimilarity drops vector hits below this score so unrelated notes
// aren't stuffed into the prompt as "relevant memories".
const MinSimilarity float32 = 0.35

type ContextResult struct {
	Context string
	Results []RetrievedNote
	// Catalog is the full vault inventory (titles + ids). Always present
	// so the model can answer "what notes do I have?" without relying on
	// semantic search matching that meta-question.
	Catalog []CatalogEntry
}

type CatalogEntry struct {
	ID    int64
	Title string
	Type  models.NoteType
	Done  bool
}

type RetrievedNote struct {
	ID         int64
	Title      string
	Path       string
	Similarity float32
	Content    string
}

func (s *Service) BuildContext(
	ctx context.Context,
	query string,
	topK int,
) (*ContextResult, error) {

	out := &ContextResult{}

	catalog, err := s.buildCatalog()
	if err != nil {
		return nil, err
	}
	out.Catalog = catalog

	var builder strings.Builder
	builder.WriteString(formatCatalog(catalog))
	builder.WriteString("\n")

	// Empty vault: skip the embedding round-trip entirely.
	if len(catalog) == 0 {
		out.Context = strings.TrimSpace(builder.String())
		return out, nil
	}

	results, err := s.Search(ctx, query, topK)
	if err != nil {
		return nil, err
	}

	var relevant []string
	seen := map[int64]bool{}

	for _, r := range results {
		if r.Score < MinSimilarity {
			continue
		}

		note, err := s.noteStore.GetByID(r.Chunk.NoteID)
		if err != nil || note == nil {
			continue
		}
		// One hit per note — keep the highest-scoring chunk (results are
		// already ranked descending).
		if seen[note.ID] {
			continue
		}
		seen[note.ID] = true

		out.Results = append(out.Results, RetrievedNote{
			ID:         note.ID,
			Title:      note.Title,
			Path:       note.Path,
			Similarity: r.Score,
			Content:    r.Chunk.Content,
		})

		relevant = append(relevant, fmt.Sprintf(
			"--- note_id=%d | %s (similarity %.2f) ---\n%s",
			note.ID,
			note.Title,
			r.Score,
			r.Chunk.Content,
		))
	}

	if len(relevant) == 0 {
		builder.WriteString("No related notes found for this query.\n")
	} else {
		builder.WriteString("Relevant notes from the user's vault:\n\n")
		builder.WriteString(strings.Join(relevant, "\n\n"))
		builder.WriteString("\n")
	}

	out.Context = strings.TrimSpace(builder.String())
	return out, nil
}

func (s *Service) buildCatalog() ([]CatalogEntry, error) {
	notes, err := s.noteStore.All()
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	out := make([]CatalogEntry, 0, len(notes))
	for _, n := range notes {
		out = append(out, CatalogEntry{
			ID:    n.ID,
			Title: n.Title,
			Type:  n.Type,
			Done:  n.Done,
		})
	}
	return out, nil
}

func formatCatalog(catalog []CatalogEntry) string {
	if len(catalog) == 0 {
		return "Vault inventory: empty (0 notes). The user has not created any notes yet."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Vault inventory (%d notes). Use note_id when updating:\n", len(catalog)))
	for _, e := range catalog {
		kind := string(e.Type)
		if kind == "" {
			kind = "note"
		}
		extra := ""
		if e.Type == models.NoteTypeTask {
			if e.Done {
				extra = ", done"
			} else {
				extra = ", open"
			}
		}
		b.WriteString(fmt.Sprintf("- note_id=%d | %s (%s%s)\n", e.ID, e.Title, kind, extra))
	}
	return b.String()
}
