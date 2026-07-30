package notes

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/utils"
)

type Service struct {
	vaultPath  string
	noteStore  *storage.NoteStore
	chunkStore *storage.ChunkStore
	ai         *ai.Client
}

func NewService(vaultPath string, noteStore *storage.NoteStore, chunkStore *storage.ChunkStore, aiClient *ai.Client) *Service {
	return &Service{vaultPath: vaultPath, noteStore: noteStore, chunkStore: chunkStore, ai: aiClient}
}

// CreateNote writes a new .md file into the vault, saves it to SQLite, and
// embeds it in chunks so it becomes searchable immediately.
func (s *Service) CreateNote(ctx context.Context, title, body string, tags []string) (*models.Note, error) {
	slug := utils.Slugify(title)
	path := filepath.Join(s.vaultPath, slug+".md")

	content, err := parser.RenderMarkdown(parser.Frontmatter{Title: title, Tags: tags}, body)
	if err != nil {
		return nil, fmt.Errorf("render markdown: %w", err)
	}

	if err := utils.WriteNoteFile(path, content); err != nil {
		return nil, fmt.Errorf("write note file: %w", err)
	}

	n := &models.Note{Title: title, Path: path, Content: body, Type: models.NoteTypeNote}
	id, err := s.noteStore.Create(n)
	if err != nil {
		return nil, fmt.Errorf("save note record: %w", err)
	}
	n.ID = id

	if err := s.embedNote(ctx, n); err != nil {
		// Note itself is saved fine; embedding failure shouldn't lose the note,
		// just means it won't show up in search until retried.
		return n, fmt.Errorf("note saved, but embedding failed: %w", err)
	}
	return n, nil
}

// embedNote chunks the note's body and stores an embedding per chunk.
func (s *Service) embedNote(ctx context.Context, n *models.Note) error {
	chunks := parser.ChunkText(n.Content, 200, 40)
	for i, text := range chunks {
		vec, err := s.ai.Embed(ctx, text)
		if err != nil {
			return fmt.Errorf("embed chunk %d: %w", i, err)
		}
		_, err = s.chunkStore.Create(&models.Chunk{
			NoteID:    n.ID,
			Content:   text,
			ChunkIdx:  i,
			Embedding: vec,
		})
		if err != nil {
			return fmt.Errorf("store chunk %d: %w", i, err)
		}
	}
	return nil
}
