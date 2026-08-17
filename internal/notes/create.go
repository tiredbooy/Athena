package notes

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
	ai         ai.EmbeddingProvider
	jobs       *storage.JobStore
}

func NewService(vaultPath string, noteStore *storage.NoteStore, chunkStore *storage.ChunkStore, aiClient ai.EmbeddingProvider) *Service {
	return &Service{vaultPath: vaultPath, noteStore: noteStore, chunkStore: chunkStore, ai: aiClient}
}

// TrackJobsIn records long-running work — currently only Reindex — in the jobs
// table, and returns the same Service so wiring reads as one expression.
//
// It is deliberately not a NewService parameter: a job record is bookkeeping,
// not something a reindex needs to run, and callers that only create and read
// notes should not have to supply a store they never use.
func (s *Service) TrackJobsIn(jobStore *storage.JobStore) *Service {
	s.jobs = jobStore
	return s
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
// Re-creating the SAME title returns the existing note with created=false
// instead of erroring — small models often re-emit create_note when the user
// only asks to list notes. A different title that slugifies onto the same
// filename ("Go Slices" vs "Go: Slices") is an error, not a duplicate.
//
// folder must already exist. Athena never invents directories for note
// writes; see docs/notes/README.md.
func (s *Service) CreateNote(ctx context.Context, title, body, folder string, tags []string) (note *models.Note, created bool, err error) {
	return s.createNote(ctx, title, body, folder, parser.Frontmatter{Title: title, Tags: tags}, models.NoteTypeNote)
}

func (s *Service) createNote(ctx context.Context, title, body, folder string, frontmatter parser.Frontmatter, noteType models.NoteType) (note *models.Note, created bool, err error) {
	if title == "" {
		return nil, false, fmt.Errorf("title is required")
	}

	cleanFolder, err := utils.CleanFolder(folder)
	if err != nil {
		return nil, false, err
	}
	if cleanFolder != "" {
		exists, err := utils.FolderExists(s.vaultPath, cleanFolder)
		if err != nil {
			return nil, false, fmt.Errorf("check destination folder: %w", err)
		}
		if !exists {
			return nil, false, fmt.Errorf("destination folder %q does not exist; create it explicitly first", cleanFolder)
		}
	}

	path, err := utils.NotePath(s.vaultPath, cleanFolder, title)
	if err != nil {
		return nil, false, err
	}

	if existing, err := s.noteStore.GetByPath(path); err != nil {
		return nil, false, err
	} else if existing != nil {
		// Re-creating the same title stays idempotent (see CreateNote), but a
		// *different* title that slugifies to the same filename is a different
		// note: returning the old one would silently drop the new content.
		// Books are exempt — a catalog title differs from the typed title only
		// in punctuation, and CreateBook repairs that note in place.
		if noteType != models.NoteTypeBook && !strings.EqualFold(strings.TrimSpace(existing.Title), strings.TrimSpace(title)) {
			return nil, false, fmt.Errorf("%q and the existing note %q both become %s; rename one of them", title, existing.Title, utils.RelVault(s.vaultPath, path))
		}
		return existing, false, nil
	}

	// Disk file without a DB row (manual vault edit) — don't clobber it.
	if _, statErr := os.Stat(path); statErr == nil {
		return nil, false, fmt.Errorf("file already exists at %s (not in database — import or rename)", utils.RelVault(s.vaultPath, path))
	}

	// Title and type are written into the YAML as well as the row (V-06). The
	// row is Athena's index, but the file is the durable record: a vault opened
	// in Obsidian, or a database rebuilt from scratch, has only the Markdown.
	frontmatter.Title = title
	frontmatter.Kind = string(noteType)
	content, err := parser.RenderMarkdown(frontmatter, body)
	if err != nil {
		return nil, false, fmt.Errorf("render markdown: %w", err)
	}

	if err := utils.WriteNoteFile(path, content); err != nil {
		return nil, false, fmt.Errorf("write note file: %w", err)
	}

	n := &models.Note{Title: title, Path: path, Content: body, Type: noteType}
	id, err := s.noteStore.Create(n)
	if err != nil {
		// Compensating undo: the file and the row are not one transaction, and
		// a file with no row is invisible to Athena forever. Removing it is
		// safe here and only here — the checks above proved nothing existed at
		// this path before this call wrote it.
		if undoErr := os.Remove(path); undoErr != nil {
			return nil, false, fmt.Errorf("save note record: %w; the new file at %s could not be removed: %v", err, utils.RelVault(s.vaultPath, path), undoErr)
		}
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

	if _, err := os.Stat(n.Path); err != nil {
		if os.IsNotExist(err) {
			if err := s.reconcileMissingNotePath(n); err != nil {
				return nil, err
			}
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect source file: %w", err)
		}
	}

	cleanFolder, err := utils.CleanFolder(folder)
	if err != nil {
		return nil, err
	}
	if cleanFolder != "" {
		exists, err := utils.FolderExists(s.vaultPath, cleanFolder)
		if err != nil {
			return nil, fmt.Errorf("check destination folder: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("destination folder %q does not exist; create it explicitly first", cleanFolder)
		}
	}

	newPath, err := utils.NotePath(s.vaultPath, cleanFolder, n.Title)
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

	oldPath := n.Path
	if err := utils.MoveFile(oldPath, newPath); err != nil {
		return nil, fmt.Errorf("move file: %w", err)
	}

	n.Path = newPath
	if err := s.saveMovedNote(n, oldPath); err != nil {
		return nil, fmt.Errorf("update path: %w", err)
	}
	return n, nil
}

// reconcileMissingNotePath repairs a record after a user moves a note outside
// Athena. Matching both filename and frontmatter title prevents a same-named
// unrelated file from being adopted; ambiguous matches are left untouched.
func (s *Service) reconcileMissingNotePath(n *models.Note) error {
	filename := filepath.Base(n.Path)
	var matches []string
	err := filepath.WalkDir(s.vaultPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			rel := utils.RelVault(s.vaultPath, path)
			if rel == ".obsidian" || rel == ".trash" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != filename {
			return nil
		}
		raw, err := utils.ReadNoteFile(path)
		if err != nil {
			return err
		}
		frontmatter, _, err := parser.ParseMarkdown(raw)
		if err != nil {
			return nil
		}
		if frontmatter.Title == n.Title {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("search vault for moved note: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("source file is missing at %s and no matching moved note was found", utils.RelVault(s.vaultPath, n.Path))
	}
	if len(matches) > 1 {
		return fmt.Errorf("source file is missing at %s and %d matching files were found; move it manually to avoid choosing the wrong note", utils.RelVault(s.vaultPath, n.Path), len(matches))
	}
	n.Path = matches[0]
	if err := s.noteStore.Update(n); err != nil {
		return fmt.Errorf("repair moved note path: %w", err)
	}
	return nil
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
