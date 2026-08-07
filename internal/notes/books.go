package notes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// CreateBook writes a structured, Obsidian-readable book note. The caller owns
// resolving catalog data; this service remains the sole owner of vault writes.
func (s *Service) CreateBook(ctx context.Context, metadata models.BookMetadata, folder string, startedAt time.Time) (*models.Note, bool, error) {
	title := strings.TrimSpace(metadata.Title)
	if title == "" {
		return nil, false, fmt.Errorf("book title is required")
	}
	fm := parser.Frontmatter{
		Title: title, Tags: []string{"book"}, Kind: "book", Authors: metadata.Authors,
		Genres: metadata.Genres, PublishedYear: metadata.PublishedYear, ISBN: metadata.ISBN,
		MetadataSource: metadata.Source, StartedAt: startedAt,
	}
	body := "# Reading notes\n\n"
	return s.createNote(ctx, title, body, folder, fm, models.NoteTypeBook)
}

// FinishBook records the completion time from Athena's clock, rather than from
// model-generated text. This makes the date trustworthy and timezone-aware.
func (s *Service) FinishBook(ctx context.Context, noteID int64, finishedAt time.Time) error {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return fmt.Errorf("load book: %w", err)
	}
	if n == nil {
		return fmt.Errorf("book %d not found", noteID)
	}
	if n.Type != models.NoteTypeBook {
		return fmt.Errorf("note %d is not a book", noteID)
	}
	raw, err := utils.ReadNoteFile(n.Path)
	if err != nil {
		return fmt.Errorf("read book file: %w", err)
	}
	fm, body, err := parser.ParseMarkdown(raw)
	if err != nil {
		return fmt.Errorf("parse book frontmatter: %w", err)
	}
	fm.FinishedAt = &finishedAt
	content, err := parser.RenderMarkdown(fm, body)
	if err != nil {
		return fmt.Errorf("render book markdown: %w", err)
	}
	if err := utils.OverwriteNoteFile(n.Path, content); err != nil {
		return fmt.Errorf("write book file: %w", err)
	}
	n.Content = body
	if err := s.noteStore.Update(n); err != nil {
		return fmt.Errorf("save book completion: %w", err)
	}
	if err := s.chunkStore.DeleteByNoteID(n.ID); err != nil {
		return fmt.Errorf("clear old chunks: %w", err)
	}
	if err := s.embedNote(ctx, n); err != nil {
		return fmt.Errorf("book was finished, but re-embedding failed: %w", err)
	}
	return nil
}

// IsBookFinished re-reads the durable Markdown frontmatter used by Obsidian.
// Agent verification uses this instead of trusting the write handler's return
// value or the model's claim.
func (s *Service) IsBookFinished(noteID int64) (bool, error) {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return false, fmt.Errorf("load book: %w", err)
	}
	if n == nil {
		return false, fmt.Errorf("book %d not found", noteID)
	}
	if n.Type != models.NoteTypeBook {
		return false, fmt.Errorf("note %d is not a book", noteID)
	}
	raw, err := utils.ReadNoteFile(n.Path)
	if err != nil {
		return false, fmt.Errorf("read book file: %w", err)
	}
	fm, _, err := parser.ParseMarkdown(raw)
	if err != nil {
		return false, fmt.Errorf("parse book frontmatter: %w", err)
	}
	return fm.FinishedAt != nil, nil
}
