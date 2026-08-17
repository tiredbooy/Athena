package notes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/utils"
)

// saveMovedNote persists a note whose file this call has just moved from
// movedFrom to n.Path. The filesystem and SQLite are not one transaction, so a
// failed row update is compensated by moving the file back — otherwise the row
// keeps pointing at a path that no longer holds the note.
//
// Only the move this call performed is undone, and utils.MoveFile refuses to
// clobber anything that reappeared at the old path, so the undo can never
// destroy user data. If the undo also fails, both errors are reported: the
// vault and the database really have diverged, and hiding either half of that
// makes it unfixable.
func (s *Service) saveMovedNote(n *models.Note, movedFrom string) error {
	err := s.noteStore.Update(n)
	if err == nil || movedFrom == "" || movedFrom == n.Path {
		return err
	}
	if undoErr := utils.MoveFile(n.Path, movedFrom); undoErr != nil {
		return fmt.Errorf("%w; the file is still at %s and could not be moved back to %s: %v", err, n.Path, movedFrom, undoErr)
	}
	n.Path = movedFrom
	return err
}

// RenameNote changes a note's title and re-slugs its on-disk filename,
// keeping it in the same folder. Content is untouched.
func (s *Service) RenameNote(noteID int64, newTitle string) (*models.Note, error) {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return nil, fmt.Errorf("note %d not found", noteID)
	}
	newTitle = strings.TrimSpace(newTitle)
	if newTitle == "" {
		return nil, fmt.Errorf("new title is required")
	}
	if _, err := os.Stat(n.Path); err != nil {
		if os.IsNotExist(err) {
			if err := s.reconcileMissingNotePath(n); err != nil {
				return nil, fmt.Errorf("locate source note before rename: %w", err)
			}
		} else {
			return nil, fmt.Errorf("inspect source file before rename: %w", err)
		}
	}

	folder := utils.RelVault(s.vaultPath, filepath.Dir(n.Path))
	if folder == "." {
		folder = ""
	}
	newPath, err := utils.NotePath(s.vaultPath, folder, newTitle)
	if err != nil {
		return nil, err
	}

	movedFrom := ""
	if newPath != n.Path {
		if err := utils.MoveFile(n.Path, newPath); err != nil {
			return nil, fmt.Errorf("rename file: %w", err)
		}
		movedFrom, n.Path = n.Path, newPath
	}
	n.Title = newTitle
	if err := s.saveMovedNote(n, movedFrom); err != nil {
		return nil, fmt.Errorf("save renamed note: %w", err)
	}
	// The YAML is rewritten last, on purpose. Until the row is saved the move
	// may still be undone, and a file put back at its old path carrying its new
	// title would disagree with the record it was just restored to match.
	if err := s.syncNoteFrontmatter(n); err != nil {
		return n, fmt.Errorf("note renamed, but its YAML title was not updated: %w", err)
	}
	return n, nil
}

// DuplicateNote copies a note's content into a new file/DB row and embeds
// it. newTitle defaults to "<title> (copy)"; folder defaults to the
// source's folder.
func (s *Service) DuplicateNote(ctx context.Context, noteID int64, newTitle, folder string) (*models.Note, error) {
	src, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	if src == nil {
		return nil, fmt.Errorf("note %d not found", noteID)
	}
	// Duplicating now reads the source file, so a row left stale by a move made
	// outside Athena has to be repaired first — the same reconciliation rename
	// and move already do. It also fixes the default folder below, which is
	// derived from the source's path.
	if _, err := os.Stat(src.Path); os.IsNotExist(err) {
		if err := s.reconcileMissingNotePath(src); err != nil {
			return nil, fmt.Errorf("locate source note before duplicating: %w", err)
		}
	}

	if newTitle = strings.TrimSpace(newTitle); newTitle == "" {
		newTitle = src.Title + " (copy)"
	}
	if folder == "" {
		folder = utils.RelVault(s.vaultPath, filepath.Dir(src.Path))
		if folder == "." {
			folder = ""
		}
	}

	// Copying the source's YAML block, not just its body, is what makes a
	// duplicated book still a book on disk: authors, ISBN, started_at and the
	// `kind: book` line live only in the file (V-06). Rebuilding the block from
	// the row would silently produce a plain note wearing a book's title.
	frontmatter, body, err := s.parseNoteFile(src.Path)
	if err != nil {
		return nil, fmt.Errorf("read source note: %w", err)
	}

	dup, created, err := s.createNote(ctx, newTitle, body, folder, frontmatter, src.Type)
	if err != nil {
		return nil, fmt.Errorf("duplicate note: %w", err)
	}
	if !created {
		return nil, fmt.Errorf("a note already exists at the duplicate's target path — choose a different title or folder")
	}

	if !src.Done {
		return dup, nil
	}
	dup.Done = true
	if err := s.noteStore.Update(dup); err != nil {
		return dup, fmt.Errorf("duplicate saved, but copying its done state failed: %w", err)
	}
	return dup, nil
}

// TrashNote moves a note's file into a hidden .trash/ folder that mirrors
// its original vault-relative path, and marks it trashed. The DB row and
// its chunks/embeddings are kept as-is so RestoreNote is a pure reversal;
// trashed notes are excluded from normal listings via NoteStore.All.
func (s *Service) TrashNote(noteID int64) (*models.Note, error) {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return nil, fmt.Errorf("note %d not found", noteID)
	}
	if n.TrashedFrom != "" {
		return n, nil
	}
	// Archive and trash are both "move away and remember where from", so they
	// cannot stack: the second move would record a path that is itself already a
	// relocation (.trash/archive/...), and restoring would put the note back
	// somewhere it never lived. The note must leave one state before entering
	// the other.
	if n.Archived {
		return nil, fmt.Errorf("note %q is archived; unarchive it before trashing", n.Title)
	}

	rel := utils.RelVault(s.vaultPath, n.Path)
	trashPath := filepath.Join(s.vaultPath, ".trash", filepath.FromSlash(rel))

	if err := utils.MoveFile(n.Path, trashPath); err != nil {
		return nil, fmt.Errorf("move to trash: %w", err)
	}

	movedFrom := n.Path
	n.TrashedFrom = rel
	n.Path = trashPath
	if err := s.saveMovedNote(n, movedFrom); err != nil {
		return nil, fmt.Errorf("mark note trashed: %w", err)
	}
	return n, nil
}

// RestoreNote moves a trashed note back to its original path.
func (s *Service) RestoreNote(noteID int64) (*models.Note, error) {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return nil, fmt.Errorf("note %d not found", noteID)
	}
	if n.TrashedFrom == "" {
		return n, nil
	}

	origPath := filepath.Join(s.vaultPath, filepath.FromSlash(n.TrashedFrom))
	if err := utils.MoveFile(n.Path, origPath); err != nil {
		return nil, fmt.Errorf("restore from trash: %w", err)
	}

	movedFrom := n.Path
	n.Path = origPath
	n.TrashedFrom = ""
	if err := s.saveMovedNote(n, movedFrom); err != nil {
		return nil, fmt.Errorf("clear trashed marker: %w", err)
	}
	return n, nil
}

// ArchiveNote moves a note into archive/<original relative path>, keeping
// it out of the working folders but still listed and searchable.
func (s *Service) ArchiveNote(noteID int64) (*models.Note, error) {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return nil, fmt.Errorf("note %d not found", noteID)
	}
	if n.Archived {
		return n, nil
	}
	// The other half of the no-stacking rule stated in TrashNote.
	if n.TrashedFrom != "" {
		return nil, fmt.Errorf("note %q is in the trash; restore it before archiving", n.Title)
	}

	rel := utils.RelVault(s.vaultPath, n.Path)
	archivePath := filepath.Join(s.vaultPath, "archive", filepath.FromSlash(rel))

	if err := utils.MoveFile(n.Path, archivePath); err != nil {
		return nil, fmt.Errorf("move to archive: %w", err)
	}

	movedFrom := n.Path
	n.Archived = true
	n.ArchivedFrom = rel
	n.Path = archivePath
	if err := s.saveMovedNote(n, movedFrom); err != nil {
		return nil, fmt.Errorf("mark note archived: %w", err)
	}
	return n, nil
}

// UnarchiveNote moves an archived note back to its original folder.
func (s *Service) UnarchiveNote(noteID int64) (*models.Note, error) {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return nil, fmt.Errorf("note %d not found", noteID)
	}
	if !n.Archived {
		return n, nil
	}

	origPath := filepath.Join(s.vaultPath, filepath.FromSlash(n.ArchivedFrom))
	if err := utils.MoveFile(n.Path, origPath); err != nil {
		return nil, fmt.Errorf("restore from archive: %w", err)
	}

	movedFrom := n.Path
	n.Path = origPath
	n.Archived = false
	n.ArchivedFrom = ""
	if err := s.saveMovedNote(n, movedFrom); err != nil {
		return nil, fmt.Errorf("clear archived marker: %w", err)
	}
	return n, nil
}
