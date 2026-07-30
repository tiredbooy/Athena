package retrieval

import (
	"context"
	"fmt"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/storage"
)

type Service struct {
	noteStore  *storage.NoteStore
	chunkStore *storage.ChunkStore
	ai         *ai.Client
}

func NewService(noteStore *storage.NoteStore, chunkStore *storage.ChunkStore, aiClient *ai.Client) *Service {
	return &Service{noteStore: noteStore, chunkStore: chunkStore, ai: aiClient}
}

// Search embeds the query text and finds the topK most similar chunks
// across the whole vault.
func (s *Service) Search(ctx context.Context, query string, topK int) ([]models.ChunkResult, error) {
	vec, err := s.ai.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	results, err := s.chunkStore.SearchSimilar(vec, topK)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	return results, nil
}
