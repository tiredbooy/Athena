package models

// Chunk is a slice of a note's content, small enough to embed meaningfully.
// One note can have many chunks; each chunk has its own embedding vector.
type Chunk struct {
	ID        int64
	NoteID    int64
	Content   string
	ChunkIdx  int
	Embedding []float32
}

type ChunkResult struct {
	Chunk
	Score float32
}