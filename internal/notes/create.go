package notes

import (
	"context"
	"fmt"
	"os"
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

func (s *Service) VaultPath() string { return s.vaultPath }

// GetNote and GetNoteByPath are narrow read APIs for post-write verification.
// Callers receive the persisted record rather than relying on a write method's
// in-memory value as proof that the database update completed.
func (s *Service) GetNote(noteID int64) (*models.Note, error) {
	return s.noteStore.GetByID(noteID)
}

func (s *Service) GetNoteByPath(path string) (*models.Note, error) {
	return s.noteStore.GetByPath(path)
}

// CreateNote writes a new .md file into the vault (optionally under folder),
// saves it to SQLite, and embeds it so it is searchable immediately.
//
// If a note already exists at the target path, it returns that note with
// created=false instead of erroring — small models often re-emit create_note
// when the user only asks to list notes.
func (s *Service) CreateNote(ctx context.Context, title, body, folder string, tags []string) (note *models.Note, created bool, err error) {
	if title == "" {
		return nil, false, fmt.Errorf("title is required")
	}

	path, err := utils.NotePath(s.vaultPath, folder, title)
	if err != nil {
		return nil, false, err
	}

	if existing, err := s.noteStore.GetByPath(path); err != nil {
		return nil, false, err
	} else if existing != nil {
		return existing, false, nil
	}

	// Disk file without a DB row (manual vault edit) — don't clobber it.
	if _, statErr := os.Stat(path); statErr == nil {
		return nil, false, fmt.Errorf("file already exists at %s (not in database — import or rename)", utils.RelVault(s.vaultPath, path))
	}

	if err := utils.EnsureDir(s.vaultPath, folder); err != nil {
		return nil, false, fmt.Errorf("create folder: %w", err)
	}

	content, err := parser.RenderMarkdown(parser.Frontmatter{Title: title, Tags: tags}, body)
	if err != nil {
		return nil, false, fmt.Errorf("render markdown: %w", err)
	}

	if err := utils.WriteNoteFile(path, content); err != nil {
		return nil, false, fmt.Errorf("write note file: %w", err)
	}

	n := &models.Note{Title: title, Path: path, Content: body, Type: models.NoteTypeNote}
	id, err := s.noteStore.Create(n)
	if err != nil {
		return nil, false, fmt.Errorf("save note record: %w", err)
	}
	n.ID = id

	if err := s.embedNote(ctx, n); err != nil {
		// Note itself is saved; embedding failure shouldn't lose it.
		return n, true, fmt.Errorf("note saved, but embedding failed: %w", err)
	}
	return n, true, nil
}

// EnsureFolders creates one or more vault-relative directories.
func (s *Service) EnsureFolders(folders []string) ([]string, error) {
	created := make([]string, 0, len(folders))
	for _, f := range folders {
		clean, err := utils.CleanFolder(f)
		if err != nil {
			return created, err
		}
		if clean == "" {
			continue
		}
		if err := utils.EnsureDir(s.vaultPath, clean); err != nil {
			return created, fmt.Errorf("create %s: %w", clean, err)
		}
		created = append(created, clean)
	}
	return created, nil
}

// MoveNote relocates a note file into folder (vault-relative) and updates DB.
func (s *Service) MoveNote(ctx context.Context, noteID int64, folder string) (*models.Note, error) {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return nil, fmt.Errorf("note %d not found", noteID)
	}

	newPath, err := utils.NotePath(s.vaultPath, folder, n.Title)
	if err != nil {
		return nil, err
	}
	if newPath == n.Path {
		return n, nil
	}

	if existing, err := s.noteStore.GetByPath(newPath); err != nil {
		return nil, err
	} else if existing != nil && existing.ID != n.ID {
		return nil, fmt.Errorf("a note already exists at %s", utils.RelVault(s.vaultPath, newPath))
	}

	if err := utils.EnsureDir(s.vaultPath, folder); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(n.Path, newPath); err != nil {
		return nil, fmt.Errorf("move file: %w", err)
	}

	n.Path = newPath
	if err := s.noteStore.Update(n); err != nil {
		return nil, fmt.Errorf("update path: %w", err)
	}
	return n, nil
}

// ListNotes returns every note for slash-command display.
func (s *Service) ListNotes() ([]*models.Note, error) {
	return s.noteStore.All()
}

// embedNote chunks the note's body and stores an embedding per chunk.
func (s *Service) embedNote(ctx context.Context, n *models.Note) error {
	chunks := parser.ChunkText(n.Content, 200, 40)
	// Empty body still gets one searchable chunk from the title so the
	// note shows up in vector search.
	texts := chunks
	if len(texts) == 0 {
		texts = []string{n.Title}
	}
	for i, text := range texts {
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
