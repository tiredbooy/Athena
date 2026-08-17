package notes

import (
	"context"
	"fmt"
	"strings"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// UpdateNote overwrites the note's body. Prefer AppendNote or ReplaceSection
// for model-driven edits: their smaller scope makes accidental loss less
// likely. Full replacement remains for an explicit user request.
func (s *Service) UpdateNote(ctx context.Context, noteID int64, newBody string) error {
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return fmt.Errorf("note %d not found", noteID)
	}

	raw, err := utils.ReadNoteFile(n.Path)
	if err != nil {
		return fmt.Errorf("read note file: %w", err)
	}
	frontmatter, _, err := parser.ParseMarkdown(raw)
	if err != nil {
		return fmt.Errorf("parse note frontmatter: %w", err)
	}
	// Every rewrite restates title and type so the file keeps describing itself
	// (V-06). Book fields already in the block are carried through untouched, as
	// is `done:` — a body edit is not a completion change, and the file's flag is
	// the durable one, so an edit must not overwrite a tick made in Obsidian.
	frontmatter.Title = n.Title
	frontmatter.Kind = string(n.Type)
	content, err := parser.RenderMarkdown(frontmatter, newBody)
	if err != nil {
		return fmt.Errorf("render markdown: %w", err)
	}
	if err := utils.OverwriteNoteFile(n.Path, content); err != nil {
		return fmt.Errorf("write note file: %w", err)
	}

	n.Content = newBody
	if err := s.noteStore.Update(n); err != nil {
		// Same compensating undo as a failed move (notes.saveMovedNote): the
		// file already holds the new body, so leaving it there would make the
		// file and the row disagree about the note's content. raw is exactly
		// what this call overwrote, so putting it back restores the state the
		// caller started from.
		if undoErr := utils.OverwriteNoteFile(n.Path, raw); undoErr != nil {
			return fmt.Errorf("save note record: %w; the file already holds the new body and could not be restored: %v", err, undoErr)
		}
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

// AppendNote adds a distinct paragraph to a note, preserving existing text.
func (s *Service) AppendNote(ctx context.Context, noteID int64, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("append content is required")
	}
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return fmt.Errorf("note %d not found", noteID)
	}
	body, err := s.readNoteBody(n.Path)
	if err != nil {
		return err
	}
	body = strings.TrimRight(body, "\n")
	if body != "" {
		body += "\n\n"
	}
	return s.UpdateNote(ctx, noteID, body+content)
}

// ReplaceSection changes one Markdown heading section only when the current
// section body matches expectedContent. The guard prevents a stale model plan
// from clobbering a user edit made after the model read the note.
func (s *Service) ReplaceSection(ctx context.Context, noteID int64, section, expectedContent, replacement string) error {
	if strings.TrimSpace(section) == "" {
		return fmt.Errorf("section is required")
	}
	n, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return fmt.Errorf("load note: %w", err)
	}
	if n == nil {
		return fmt.Errorf("note %d not found", noteID)
	}

	body, err := s.readNoteBody(n.Path)
	if err != nil {
		return err
	}
	updated, current, ok := replaceMarkdownSection(body, section, replacement)
	if !ok {
		return fmt.Errorf("section %q not found", section)
	}
	if strings.TrimSpace(current) != strings.TrimSpace(expectedContent) {
		return fmt.Errorf("section %q changed since it was read; no update was made", section)
	}
	return s.UpdateNote(ctx, noteID, updated)
}

// parseNoteFile splits a note's file into its durable YAML block and its body.
// Write paths that must preserve metadata they do not understand — book fields,
// an Obsidian-authored tag list — read it from here rather than rebuilding a
// Frontmatter from the SQLite row, which only knows title, type and done.
func (s *Service) parseNoteFile(path string) (parser.Frontmatter, string, error) {
	raw, err := utils.ReadNoteFile(path)
	if err != nil {
		return parser.Frontmatter{}, "", fmt.Errorf("read note file: %w", err)
	}
	frontmatter, body, err := parser.ParseMarkdown(raw)
	if err != nil {
		return parser.Frontmatter{}, "", fmt.Errorf("parse note frontmatter: %w", err)
	}
	return frontmatter, body, nil
}

func (s *Service) readNoteBody(path string) (string, error) {
	_, body, err := s.parseNoteFile(path)
	return body, err
}

// syncNoteFrontmatter restates the note's title, type and done state in its
// file, leaving the body byte-identical. Renaming used to change only the
// filename and the row, so Obsidian kept showing the old `title:` forever, and
// ticking a task changed nothing on disk at all (V-06).
func (s *Service) syncNoteFrontmatter(n *models.Note) error {
	frontmatter, body, err := s.parseNoteFile(n.Path)
	if err != nil {
		return err
	}
	// `done:` is a task's field (models.Note.Done is meaningless for the other
	// types), so a note or book keeps whatever its own YAML said. Restating the
	// row's always-false Done onto them would silently strip a `done:` the user
	// wrote in Obsidian.
	done := frontmatter.Done
	if n.Type == models.NoteTypeTask {
		done = n.Done
	}
	if frontmatter.Title == n.Title && frontmatter.Kind == string(n.Type) && frontmatter.Done == done {
		return nil
	}
	frontmatter.Title = n.Title
	frontmatter.Kind = string(n.Type)
	frontmatter.Done = done
	content, err := parser.RenderMarkdown(frontmatter, body)
	if err != nil {
		return fmt.Errorf("render note markdown: %w", err)
	}
	if err := utils.OverwriteNoteFile(n.Path, content); err != nil {
		return fmt.Errorf("write note file: %w", err)
	}
	return nil
}

func replaceMarkdownSection(body, section, replacement string) (updated, current string, found bool) {
	lines := strings.Split(body, "\n")
	target := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(section), "#"))
	start, level := -1, 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		currentLevel := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		if currentLevel == 0 || len(trimmed) <= currentLevel || trimmed[currentLevel] != ' ' {
			continue
		}
		if strings.TrimSpace(trimmed[currentLevel:]) == target {
			start, level = i, currentLevel
			break
		}
	}
	if start < 0 {
		return "", "", false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		currentLevel := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		if currentLevel > 0 && currentLevel <= level && len(trimmed) > currentLevel && trimmed[currentLevel] == ' ' {
			end = i
			break
		}
	}
	current = strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
	replacementLines := []string{lines[start]}
	if strings.TrimSpace(replacement) != "" {
		replacementLines = append(replacementLines, strings.TrimSpace(replacement))
	}
	merged := append([]string{}, lines[:start]...)
	merged = append(merged, replacementLines...)
	merged = append(merged, lines[end:]...)
	return strings.Join(merged, "\n"), current, true
}
