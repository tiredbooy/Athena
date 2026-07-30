package retrieval

import (
	"context"
	"fmt"
	"strings"
)

// BuildContext runs a search and formats the results into a plain-text
// block suitable for injecting into the chat system prompt, so the model
// can answer using the user's actual notes instead of guessing.
func (s *Service) BuildContext(ctx context.Context, query string, topK int) (string, error) {
	results, err := s.Search(ctx, query, topK)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No related notes found.", nil
	}

	var b strings.Builder
	b.WriteString("Relevant notes from the user's vault:\n\n")

	for _, r := range results {
		note, err := s.noteStore.GetByID(r.Chunk.NoteID)
		if err != nil || note == nil {
			continue // chunk's parent note vanished somehow; skip rather than fail the whole context
		}
		b.WriteString(fmt.Sprintf("--- %s (similarity %.2f) ---\n%s\n\n", note.Title, r.Score, r.Chunk.Content))
	}

	return b.String(), nil
}
