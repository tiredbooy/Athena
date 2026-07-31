package retrieval

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/utils"
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
	ID     int64
	Title  string
	Type   models.NoteType
	Done   bool
	Folder string // vault-relative dir, empty = vault root
	Rel    string // vault-relative file path
}

type RetrievedNote struct {
	ID         int64
	Title      string
	Path       string
	Similarity float32
	Content    string
}

// ProgressFunc receives short descriptions of retrieval work. Keeping this
// callback in the retrieval layer means callers can report progress without
// coupling retrieval to a specific terminal or graphical UI.
type ProgressFunc func(message string)

// Inventory returns the complete note catalog without doing vector search.
// It is used for exact listing requests, where embedding a query would only
// add latency and cannot improve the answer.
func (s *Service) Inventory() ([]CatalogEntry, error) {
	return s.buildCatalog()
}

func (s *Service) BuildContext(
	ctx context.Context,
	query string,
	topK int,
) (*ContextResult, error) {
	return s.BuildContextWithProgress(ctx, query, topK, nil)
}

// BuildContextWithProgress builds the prompt context and reports the actual
// vault steps it takes. A nil progress callback disables reporting.
func (s *Service) BuildContextWithProgress(
	ctx context.Context,
	query string,
	topK int,
	progress ProgressFunc,
) (*ContextResult, error) {

	out := &ContextResult{}

	reportProgress(progress, "Reading vault inventory")
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

	reportProgress(progress, "Searching note content")
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
		rel, _ := s.withRel(s.vaultPath, note.Path)
		reportProgress(progress, fmt.Sprintf("Reading %s", rel))
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

func reportProgress(progress ProgressFunc, message string) {
	if progress != nil {
		progress(message)
	}
}

func (s *Service) buildCatalog() ([]CatalogEntry, error) {
	notes, err := s.noteStore.All()
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	out := make([]CatalogEntry, 0, len(notes))
	for _, n := range notes {
		rel, folder := s.withRel(s.vaultPath, n.Path)
		out = append(out, CatalogEntry{
			ID:     n.ID,
			Title:  n.Title,
			Type:   n.Type,
			Done:   n.Done,
			Folder: folder,
			Rel:    rel,
		})
	}
	return out, nil
}

// SetVaultPath lets the service render tidy relative paths in the catalog.
// Optional — without it, absolute paths are shown.
func (s *Service) withRel(vault, abs string) (rel, folder string) {
	rel = utils.RelVault(vault, abs)
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." || dir == "" {
		return rel, ""
	}
	return rel, dir
}

func formatCatalog(catalog []CatalogEntry) string {
	if len(catalog) == 0 {
		return "Vault inventory: empty (0 notes). The user has not created any notes yet.\nIMPORTANT: If the user is only asking what notes they have, answer that the vault is empty. Do NOT create a note."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Vault inventory (%d notes). Use note_id when updating or moving.\n", len(catalog)))
	b.WriteString("IMPORTANT: If the user is only listing or asking what notes they have, answer from this inventory. Do NOT create or modify notes.\n")
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
		loc := "vault root"
		if e.Folder != "" {
			loc = e.Folder
		} else if e.Rel != "" && !strings.HasPrefix(e.Rel, "/") {
			if d := filepath.ToSlash(filepath.Dir(e.Rel)); d != "." {
				loc = d
			}
		}
		b.WriteString(fmt.Sprintf("- note_id=%d | %s | folder=%s (%s%s)\n", e.ID, e.Title, loc, kind, extra))
	}
	return b.String()
}
