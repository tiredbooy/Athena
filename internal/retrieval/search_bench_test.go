package retrieval

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/storage"
)

// embedDims matches qwen3-embedding:0.6b (config.DefaultEmbeddingModel), the
// vector width a real vault actually stores. Benchmarking narrower vectors
// would understate both the SQLite blob decode and the cosine loop.
const embedDims = 1024

// chunksPerNote follows the chunker's 200-word window: a few hundred words of
// prose per note. It only decides how many `notes` rows back the vectors, so
// the numbers stay comparable to a real vault.
const chunksPerNote = 6

// BenchmarkSearchNotes measures one interactive semantic search — embed (stub),
// brute-force cosine over every searchable chunk, then load the winning notes —
// at personal-vault scale. This is the measurement L-02 asks for before anyone
// considers an approximate vector index. Results live in
// docs/retrieval/README.md.
func BenchmarkSearchNotes(b *testing.B) {
	for _, chunkCount := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("chunks=%d", chunkCount), func(b *testing.B) {
			service, query := benchmarkService(b, chunkCount)
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				results, err := service.SearchNotes(ctx, query, 4)
				if err != nil {
					b.Fatalf("search: %v", err)
				}
				if len(results) == 0 {
					b.Fatal("benchmark corpus produced no hits above MinSimilarity")
				}
			}
		})
	}
}

// BenchmarkSearchableLoad isolates the half of a search that an approximate
// vector index would NOT remove: reading every searchable chunk out of SQLite
// and decoding its BLOB back into []float32. Compare it with
// BenchmarkSearchNotes before believing an index is the fix.
func BenchmarkSearchableLoad(b *testing.B) {
	for _, chunkCount := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("chunks=%d", chunkCount), func(b *testing.B) {
			service, _ := benchmarkService(b, chunkCount)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				loaded, err := service.chunkStore.Searchable()
				if err != nil {
					b.Fatalf("load chunks: %v", err)
				}
				if len(loaded) != chunkCount {
					b.Fatalf("loaded %d chunks, want %d", len(loaded), chunkCount)
				}
			}
		})
	}
}

// benchmarkService fills a temp SQLite database with chunkCount embedded
// chunks and returns a service wired to a stub embedder, so the benchmark
// never touches the network or a live Ollama.
func benchmarkService(b *testing.B, chunkCount int) (*Service, string) {
	b.Helper()

	vault := b.TempDir()
	db, err := storage.Open(filepath.Join(b.TempDir(), "athena.db"))
	if err != nil {
		b.Fatalf("open database: %v", err)
	}
	b.Cleanup(func() { db.Close() })

	notes := storage.NewNoteStore(db)
	chunks := storage.NewChunkStore(db)

	random := rand.New(rand.NewSource(1))
	corpus := make([]*models.Chunk, 0, chunkCount)
	for i := 0; i < chunkCount; i++ {
		if i%chunksPerNote == 0 {
			name := fmt.Sprintf("note-%d", i/chunksPerNote)
			if _, err := notes.Create(&models.Note{
				Title: name,
				Path:  filepath.Join(vault, name+".md"),
				Type:  models.NoteTypeNote,
			}); err != nil {
				b.Fatalf("create note: %v", err)
			}
		}
		corpus = append(corpus, &models.Chunk{
			NoteID:    int64(i/chunksPerNote) + 1,
			Content:   fmt.Sprintf("chunk %d", i),
			ChunkIdx:  i % chunksPerNote,
			Embedding: randomUnitVector(random),
		})
	}
	// One transaction for the whole corpus: per-chunk Create would spend the
	// setup in SQLite commits rather than in what we came here to measure.
	if err := chunks.ReplaceAll(corpus); err != nil {
		b.Fatalf("store corpus: %v", err)
	}

	// Query the first stored vector so at least one hit always clears
	// MinSimilarity — random vectors in 1024 dimensions are near-orthogonal,
	// and an all-filtered result would benchmark a different code path.
	service := NewService(vault, notes, chunks, &countingEmbedder{vector: corpus[0].Embedding})
	corpus = nil
	return service, "benchmark query"
}

func randomUnitVector(random *rand.Rand) []float32 {
	vector := make([]float32, embedDims)
	for i := range vector {
		vector[i] = float32(random.NormFloat64())
	}
	return vector
}
