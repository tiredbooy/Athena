package storage

import (
	"math"
	"sort"

	"github.com/tiredbooy/internal/models"
)

// SearchSimilar returns the topK chunks whose embeddings are closest to
// query, ranked by cosine similarity (1.0 = same direction, 0 = unrelated).
// Trashed notes are excluded: soft-deleted content must never reach RAG.
// Brute-force in memory — fine at personal-vault scale (thousands of
// chunks); revisit with a real vector index only if it ever gets huge.
func (s *ChunkStore) SearchSimilar(query []float32, topK int) ([]models.ChunkResult, error) {
	all, err := s.Searchable()
	if err != nil {
		return nil, err
	}

	results := make([]models.ChunkResult, 0, len(all))
	for _, c := range all {
		score := cosineSimilarity(query, c.Embedding)
		results = append(results, models.ChunkResult{Chunk: *c, Score: score})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })

	if topK < len(results) {
		results = results[:topK]
	}
	return results, nil
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}
