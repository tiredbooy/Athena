package notes

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/utils"
)

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

	folder := utils.RelVault(s.vaultPath, filepath.Dir(n.Path))
	if folder == "." {
		folder = ""
	}
	newPath, err := utils.NotePath(s.vaultPath, folder, newTitle)
	if err != nil {
		return nil, err
	}

	if newPath != n.Path {
		if err := utils.MoveFile(n.Path, newPath); err != nil {
			return nil, fmt.Errorf("rename file: %w", err)
		}
		n.Path = newPath
	}
	n.Title = newTitle
	if err := s.noteStore.Update(n); err != nil {
		return nil, fmt.Errorf("save renamed note: %w", err)
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

	if newTitle = strings.TrimSpace(newTitle); newTitle == "" {
		newTitle = src.Title + " (copy)"
	}
	if folder == "" {
		folder = utils.RelVault(s.vaultPath, filepath.Dir(src.Path))
		if folder == "." {
			folder = ""
		}
	}

	dup, created, err := s.CreateNote(ctx, newTitle, src.Content, folder, nil)
	if err != nil {
		return nil, fmt.Errorf("duplicate note: %w", err)
	}
	if !created {
		return nil, fmt.Errorf("a note already exists at the duplicate's target path — choose a different title or folder")
	}

	dup.Type = src.Type
	dup.Done = src.Done
	if err := s.noteStore.Update(dup); err != nil {
		return dup, fmt.Errorf("duplicate saved, but copying type/done failed: %w", err)
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

	rel := utils.RelVault(s.vaultPath, n.Path)
	trashPath := filepath.Join(s.vaultPath, ".trash", filepath.FromSlash(rel))

	if err := utils.MoveFile(n.Path, trashPath); err != nil {
		return nil, fmt.Errorf("move to trash: %w", err)
	}

	n.TrashedFrom = rel
	n.Path = trashPath
	if err := s.noteStore.Update(n); err != nil {
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

	n.Path = origPath
	n.TrashedFrom = ""
	if err := s.noteStore.Update(n); err != nil {
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

	rel := utils.RelVault(s.vaultPath, n.Path)
	archivePath := filepath.Join(s.vaultPath, "archive", filepath.FromSlash(rel))

	if err := utils.MoveFile(n.Path, archivePath); err != nil {
		return nil, fmt.Errorf("move to archive: %w", err)
	}

	n.Archived = true
	n.ArchivedFrom = rel
	n.Path = archivePath
	if err := s.noteStore.Update(n); err != nil {
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

	n.Path = origPath
	n.Archived = false
	n.ArchivedFrom = ""
	if err := s.noteStore.Update(n); err != nil {
		return nil, fmt.Errorf("clear archived marker: %w", err)
	}
	return n, nil
}
