package notes

import (
	"context"
	"fmt"

	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// UpdateNote overwrites the note's body on disk and in SQLite, then
// re-chunks and re-embeds it from scratch — simplest correct approach,
// no diffing of what changed.
func (s *Service) UpdateNote(ctx context.Context, noteID int64, newBody string) error {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return fmt.Errorf("note %d not found", noteID)
	}

	content, err := parser.RenderMarkdown(parser.Frontmatter{Title: n.Title}, newBody)
	if err != nil {
		return fmt.Errorf("render markdown: %w", err)
	}
	if err := utils.OverwriteNoteFile(n.Path, content); err != nil {
		return fmt.Errorf("write note file: %w", err)
	}

	n.Content = newBody
	if err := s.noteStore.Update(n); err != nil {
		return fmt.Errorf("save note record: %w", err)
	}

	if err := s.chunkStore.DeleteByNoteID(n.ID); err != nil {
		return fmt.Errorf("clear old chunks: %w", err)
	}
	if err := s.embedNote(ctx, n); err != nil {
		return fmt.Errorf("note updated, but re-embedding failed: %w", err)
	}
	return nil
}
