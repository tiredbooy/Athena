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
	note, created, err := s.createNote(ctx, title, body, folder, fm, models.NoteTypeBook)
	if err != nil || created || note.Type == models.NoteTypeBook {
		return note, created, err
	}
	// A previous weak-model plan may have created the same target as a generic
	// note. An explicit create_book request should repair that representation in
	// place, preserving the user's body rather than creating a duplicate file.
	if err := s.promoteNoteToBook(ctx, note, metadata, startedAt); err != nil {
		return note, false, err
	}
	return note, true, nil
}

func (s *Service) promoteNoteToBook(ctx context.Context, note *models.Note, metadata models.BookMetadata, startedAt time.Time) error {
	raw, err := utils.ReadNoteFile(note.Path)
	if err != nil {
		return fmt.Errorf("read existing book note: %w", err)
	}
	fm, body, err := parser.ParseMarkdown(raw)
	if err != nil {
		return fmt.Errorf("parse existing book note: %w", err)
	}
	fm.Title = strings.TrimSpace(metadata.Title)
	fm.Tags = appendMissingTag(fm.Tags, "book")
	fm.Kind = "book"
	fm.Authors = metadata.Authors
	fm.Genres = metadata.Genres
	fm.PublishedYear = metadata.PublishedYear
	fm.ISBN = metadata.ISBN
	fm.MetadataSource = metadata.Source
	if fm.StartedAt.IsZero() {
		fm.StartedAt = startedAt
	}
	content, err := parser.RenderMarkdown(fm, body)
	if err != nil {
		return fmt.Errorf("render existing book note: %w", err)
	}
	if err := utils.OverwriteNoteFile(note.Path, content); err != nil {
		return fmt.Errorf("write existing book note: %w", err)
	}
	note.Title = fm.Title
	note.Content = body
	note.Type = models.NoteTypeBook
	if err := s.noteStore.Update(note); err != nil {
		return fmt.Errorf("save existing book note: %w", err)
	}
	if err := s.chunkStore.DeleteByNoteID(note.ID); err != nil {
		return fmt.Errorf("clear existing book chunks: %w", err)
	}
	if err := s.embedNote(ctx, note); err != nil {
		return fmt.Errorf("book metadata was saved, but re-embedding failed: %w", err)
	}
	return nil
}

func appendMissingTag(tags []string, tag string) []string {
	for _, existing := range tags {
		if strings.EqualFold(strings.TrimSpace(existing), tag) {
			return tags
		}
	}
	return append(tags, tag)
}

// UpdateBookMetadata applies only factual values explicitly supplied by the
// user. Empty slices leave the existing frontmatter unchanged, so a partial
// fallback never erases verified catalog metadata.
func (s *Service) UpdateBookMetadata(ctx context.Context, noteID int64, authors, genres []string) error {
	note, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return fmt.Errorf("load book: %w", err)
	}
	if note == nil {
		return fmt.Errorf("book %d not found", noteID)
	}
	if note.Type != models.NoteTypeBook {
		return fmt.Errorf("note %d is not a book", noteID)
	}
	raw, err := utils.ReadNoteFile(note.Path)
	if err != nil {
		return fmt.Errorf("read book file: %w", err)
	}
	fm, body, err := parser.ParseMarkdown(raw)
	if err != nil {
		return fmt.Errorf("parse book frontmatter: %w", err)
	}
	appliedFallback := false
	if cleaned := cleanMetadataValues(authors); len(cleaned) > 0 {
		if len(fm.Authors) == 0 {
			fm.Authors = cleaned
			appliedFallback = true
		} else if !sameMetadataValues(fm.Authors, cleaned) {
			return fmt.Errorf("book %d already has author metadata; fallback values cannot replace it", noteID)
		}
	}
	if cleaned := cleanMetadataValues(genres); len(cleaned) > 0 {
		if len(fm.Genres) == 0 {
			fm.Genres = cleaned
			appliedFallback = true
		} else if !sameMetadataValues(fm.Genres, cleaned) {
			return fmt.Errorf("book %d already has genre metadata; fallback values cannot replace it", noteID)
		}
	}
	if !appliedFallback {
		return nil
	}
	if fm.MetadataSource == "" || fm.MetadataSource == "unresolved" {
		fm.MetadataSource = "user"
	} else if !strings.Contains(fm.MetadataSource, "user") {
		fm.MetadataSource += "+user"
	}
	content, err := parser.RenderMarkdown(fm, body)
	if err != nil {
		return fmt.Errorf("render book markdown: %w", err)
	}
	if err := utils.OverwriteNoteFile(note.Path, content); err != nil {
		return fmt.Errorf("write book metadata: %w", err)
	}
	note.Content = body
	if err := s.noteStore.Update(note); err != nil {
		return fmt.Errorf("save book metadata: %w", err)
	}
	if err := s.chunkStore.DeleteByNoteID(note.ID); err != nil {
		return fmt.Errorf("clear old chunks: %w", err)
	}
	if err := s.embedNote(ctx, note); err != nil {
		return fmt.Errorf("book metadata was saved, but re-embedding failed: %w", err)
	}
	return nil
}

func cleanMetadataValues(values []string) []string {
	seen := make(map[string]bool)
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func sameMetadataValues(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !strings.EqualFold(strings.TrimSpace(left[index]), strings.TrimSpace(right[index])) {
			return false
		}
	}
	return true
}

// BookMetadata re-reads the durable frontmatter for post-write verification.
func (s *Service) BookMetadata(noteID int64) (parser.Frontmatter, error) {
	note, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return parser.Frontmatter{}, fmt.Errorf("load book: %w", err)
	}
	if note == nil {
		return parser.Frontmatter{}, fmt.Errorf("book %d not found", noteID)
	}
	if note.Type != models.NoteTypeBook {
		return parser.Frontmatter{}, fmt.Errorf("note %d is not a book", noteID)
	}
	raw, err := utils.ReadNoteFile(note.Path)
	if err != nil {
		return parser.Frontmatter{}, fmt.Errorf("read book file: %w", err)
	}
	fm, _, err := parser.ParseMarkdown(raw)
	if err != nil {
		return parser.Frontmatter{}, fmt.Errorf("parse book frontmatter: %w", err)
	}
	return fm, nil
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
