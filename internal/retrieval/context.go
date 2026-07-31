package retrieval

import (
	"context"
	"fmt"
	"strings"
)

type ContextResult struct {
	Context string
	Results []RetrievedNote
}

type RetrievedNote struct {
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

	results, err := s.Search(ctx, query, topK)
	if err != nil {
		return nil, err
	}

	out := &ContextResult{}

	if len(results) == 0 {
		out.Context = "No related notes found."
		return out, nil
	}

	var builder strings.Builder

	builder.WriteString("Relevant notes from the user's vault:\n\n")

	for _, r := range results {

		note, err := s.noteStore.GetByID(r.Chunk.NoteID)
		if err != nil || note == nil {
			continue
		}

		out.Results = append(out.Results, RetrievedNote{
			Title:      note.Title,
			Path:       note.Path,
			Similarity: r.Score,
			Content:    r.Chunk.Content,
		})

		builder.WriteString(fmt.Sprintf(
			"--- %s (similarity %.2f) ---\n%s\n\n",
			note.Title,
			r.Score,
			r.Chunk.Content,
		))
	}

	out.Context = builder.String()

	return out, nil
}
