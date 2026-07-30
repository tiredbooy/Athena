package notes

import (
	"context"
	"fmt"

	"github.com/tiredbooy/internal/models"
)

// CreateTask is CreateNote with the type flipped to task — same file/DB/
// embedding pipeline, since a task is just a note that can be done/undone.
func (s *Service) CreateTask(ctx context.Context, title, body string) (*models.Note, error) {
	n, err := s.CreateNote(ctx, title, body, nil)
	if err != nil {
		return n, err
	}
	n.Type = models.NoteTypeTask
	if err := s.noteStore.Update(n); err != nil {
		return n, fmt.Errorf("mark as task: %w", err)
	}
	return n, nil
}

// MarkDone flips a task's Done flag. Doesn't touch chunks/embeddings —
// completion status isn't semantic content worth re-embedding over.
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
