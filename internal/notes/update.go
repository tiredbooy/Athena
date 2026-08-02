package notes

import (
	"context"
	"fmt"
	"strings"

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
	frontmatter.Title = n.Title
	content, err := parser.RenderMarkdown(frontmatter, newBody)
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

func (s *Service) readNoteBody(path string) (string, error) {
	raw, err := utils.ReadNoteFile(path)
	if err != nil {
		return "", fmt.Errorf("read note file: %w", err)
	}
	_, body, err := parser.ParseMarkdown(raw)
	if err != nil {
		return "", fmt.Errorf("parse note frontmatter: %w", err)
	}
	return body, nil
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
