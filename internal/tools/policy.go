package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
)

const (
	maxActionsPerBatch = 32
	readToolTimeout    = 10 * time.Second
	writeToolTimeout   = 60 * time.Second
)

type actionPolicy struct {
	timeout  time.Duration
	attempts int
}

func policyFor(actionType string) actionPolicy {
	switch actionType {
	case "list_folders", "folder_exists":
		// These actions only inspect local state, so retrying them cannot
		// duplicate a user-visible change.
		return actionPolicy{timeout: readToolTimeout, attempts: 2}
	default:
		return actionPolicy{timeout: writeToolTimeout, attempts: 1}
	}
}

// validateAction is the dispatcher boundary for model-supplied data. Note
// services still validate paths and filesystem rules; this layer rejects bad
// shapes before any handler can begin side effects.
func validateAction(action ai.Action, known bool) error {
	action.Type = strings.TrimSpace(action.Type)
	if action.Type == "" {
		return fmt.Errorf("action type is required")
	}
	if !known {
		return fmt.Errorf("unknown action type %q", action.Type)
	}

	switch action.Type {
	case "create_note", "create_task":
		if strings.TrimSpace(action.Title) == "" {
			return fmt.Errorf("%s requires title", action.Type)
		}
	case "ensure_folders":
		if len(action.Paths) == 0 {
			return fmt.Errorf("ensure_folders requires paths")
		}
	case "move_note", "update_note", "mark_done", "rename_note", "duplicate_note", "trash_note", "restore_note", "archive_note", "unarchive_note":
		if action.NoteID <= 0 {
			return fmt.Errorf("%s requires note_id", action.Type)
		}
	case "create_folder", "folder_exists", "delete_folder", "rename_folder", "move_folder":
		if strings.TrimSpace(action.Folder) == "" {
			return fmt.Errorf("%s requires folder", action.Type)
		}
	}

	switch action.Type {
	case "rename_folder", "move_folder":
		if strings.TrimSpace(action.NewFolder) == "" {
			return fmt.Errorf("%s requires new_folder", action.Type)
		}
	}
	return nil
}
