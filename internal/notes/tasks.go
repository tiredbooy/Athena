package notes

import (
	"context"
	"fmt"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
)

// CreateTask is CreateNote with the type set to task — same file/DB/embedding
// pipeline, since a task is just a note that can be done/undone. It goes
// through createNote rather than CreateNote so the type reaches the file's YAML
// in the first write; flipping it in the row afterwards left `kind: note` on
// disk, and Obsidian only ever sees the file (V-06).
func (s *Service) CreateTask(ctx context.Context, title, body, folder string) (*models.Note, bool, error) {
	return s.createNote(ctx, title, body, folder, parser.Frontmatter{}, models.NoteTypeTask)
}

// MarkDone flips a task's Done flag in the row and in the file's YAML. Doesn't
// touch chunks/embeddings — completion status isn't semantic content worth
// re-embedding over.
//
// The file matters because the row is only an index: without `done:` on disk a
// ticked task is indistinguishable from an open one in Obsidian, and rebuilding
// the database from the vault (ReconcileVault) reopens every finished task.
func (s *Service) MarkDone(taskID int64, done bool) error {
	n, err := s.noteStore.GetByID(taskID)
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}
	if n == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	if n.Type != models.NoteTypeTask {
		return fmt.Errorf("note %d is not a task", taskID)
	}

	n.Done = done
	if err := s.noteStore.Update(n); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	// YAML last, as in RenameNote: the row is what every listing reads, so a
	// file updated ahead of a row that then refuses the write would be the
	// harder half-state to explain.
	if err := s.syncNoteFrontmatter(n); err != nil {
		return fmt.Errorf("task status saved, but its file's `done:` was not updated: %w", err)
	}
	return nil
}

// OpenTasks returns all tasks that aren't done yet.
func (s *Service) OpenTasks() ([]*models.Note, error) {
	all, err := s.noteStore.All()
	if err != nil {
		return nil, fmt.Errorf("load notes: %w", err)
	}

	var open []*models.Note
	for _, n := range all {
		if n.Type == models.NoteTypeTask && !n.Done {
			open = append(open, n)
		}
	}
	return open, nil
}
