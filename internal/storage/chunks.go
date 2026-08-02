package storage

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/tiredbooy/internal/models"
)

type ChunkStore struct {
	db *sql.DB
}

func NewChunkStore(db *sql.DB) *ChunkStore {
	return &ChunkStore{db: db}
}

func (s *ChunkStore) Create(c *models.Chunk) (int64, error) {
	blob := encodeEmbedding(c.Embedding)
	res, err := s.db.Exec(
		`INSERT INTO chunks (note_id, content, chunk_index, embedding) VALUES (?, ?, ?, ?)`,
		c.NoteID, c.Content, c.ChunkIdx, blob,
	)
	if err != nil {
		return 0, fmt.Errorf("insert chunk: %w", err)
	}
	return res.LastInsertId()
}

func (s *ChunkStore) All() ([]*models.Chunk, error) {
	rows, err := s.db.Query(`SELECT id, note_id, content, chunk_index, embedding FROM chunks`)
	if err != nil {
		return nil, fmt.Errorf("query chunks: %w", err)
	}
	defer rows.Close()

	var chunks []*models.Chunk
	for rows.Next() {
		var c models.Chunk
		var blob []byte
		if err := rows.Scan(&c.ID, &c.NoteID, &c.Content, &c.ChunkIdx, &blob); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		c.Embedding = decodeEmbedding(blob)
		chunks = append(chunks, &c)
	}
	return chunks, rows.Err()
}

// encodeEmbedding packs a []float32 into raw bytes so it fits in a SQLite
// BLOB column. SQLite has no native vector/array type, so we do it by hand.
func encodeEmbedding(v []float32) []byte {
	buf := new(bytes.Buffer)
	buf.Grow(len(v) * 4)
	for _, f := range v {
		binary.Write(buf, binary.LittleEndian, math.Float32bits(f))
	}
	return buf.Bytes()
}

func decodeEmbedding(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(b[i*4 : i*4+4])
		out[i] = math.Float32frombits(bits)
	}
	return out
}

func (s *ChunkStore) DeleteByNoteID(noteID int64) error {
	_, err := s.db.Exec(`DELETE FROM chunks WHERE note_id = ?`, noteID)
	if err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}
	return nil
}

// ReplaceAll atomically swaps the whole vector index only after callers have
// prepared every new vector. A failed reindex therefore leaves the old index usable.
func (s *ChunkStore) ReplaceAll(chunks []*models.Chunk) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM chunks`); err != nil {
		return fmt.Errorf("clear vector index: %w", err)
	}
	for _, chunk := range chunks {
		if _, err := tx.Exec(`INSERT INTO chunks (note_id, content, chunk_index, embedding) VALUES (?, ?, ?, ?)`, chunk.NoteID, chunk.Content, chunk.ChunkIdx, encodeEmbedding(chunk.Embedding)); err != nil {
			return fmt.Errorf("store reindexed chunk: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vector index: %w", err)
	}
	return nil
}
