package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
)

const maxActionsPerBatch = 32

type ToolKind string

const (
	ToolRead        ToolKind = "read"
	ToolWrite       ToolKind = "write"
	ToolDestructive ToolKind = "destructive"
)

// Policy is the single safety contract for a model-requested action. New
// actions must be added here before they can be considered production-ready.
type Policy struct {
	Kind                 ToolKind
	Timeout              time.Duration
	RetrySafe            bool
	RequiresConfirmation bool
	ParallelSafe         bool
}

var actionPolicies = map[string]Policy{
	"list_folders":  {Kind: ToolRead, Timeout: 10 * time.Second, RetrySafe: true, ParallelSafe: true},
	"folder_exists": {Kind: ToolRead, Timeout: 10 * time.Second, RetrySafe: true, ParallelSafe: true},

	"create_note":     {Kind: ToolWrite, Timeout: time.Minute},
	"create_task":     {Kind: ToolWrite, Timeout: time.Minute},
	"ensure_folders":  {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"move_note":       {Kind: ToolWrite, Timeout: time.Minute},
	"append_note":     {Kind: ToolWrite, Timeout: time.Minute},
	"replace_section": {Kind: ToolWrite, Timeout: time.Minute},
	"mark_done":       {Kind: ToolWrite, Timeout: time.Minute},
	"create_folder":   {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"link_folders":    {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"unlink_folders":  {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	// This only adds missing Athena-owned visual groups in .obsidian/graph.json.
	// SyncFolderGraph already writes the same settings after structural changes,
	// so a single explicit color request is safe to apply directly.
	"set_folder_colors":    {Kind: ToolWrite, Timeout: time.Minute},
	"set_graph_node_size":  {Kind: ToolWrite, Timeout: time.Minute},
	"rename_note":          {Kind: ToolWrite, Timeout: time.Minute},
	"duplicate_note":       {Kind: ToolWrite, Timeout: time.Minute},
	"restore_note":         {Kind: ToolWrite, Timeout: time.Minute},
	"archive_note":         {Kind: ToolWrite, Timeout: time.Minute},
	"unarchive_note":       {Kind: ToolWrite, Timeout: time.Minute},
	"create_book":          {Kind: ToolWrite, Timeout: time.Minute},
	"update_book_metadata": {Kind: ToolWrite, Timeout: time.Minute},
	"finish_book":          {Kind: ToolWrite, Timeout: time.Minute},

	"update_note":   {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
	"trash_note":    {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
	"delete_folder": {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
	"rename_folder": {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
	"move_folder":   {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
}

// PolicyFor returns the declared policy for a built-in action. The boolean is
// false for an extension that has not yet declared its safety contract.
func PolicyFor(actionType string) (Policy, bool) {
	policy, ok := actionPolicies[strings.TrimSpace(actionType)]
	return policy, ok
}

func policyFor(actionType string) Policy {
	if policy, ok := PolicyFor(actionType); ok {
		return policy
	}
	// Custom handlers used by callers/tests default to the safest behavior:
	// a bounded, non-retriable, serial write.
	return Policy{Kind: ToolWrite, Timeout: time.Minute}
}

// RequiresConfirmation keeps review policy out of UI/session code. Any batch
// containing a write is considered broad enough to review as one plan.
func RequiresConfirmation(actions []ai.Action) bool {
	if len(actions) > 1 {
		for _, action := range actions {
			if policyFor(action.Type).Kind != ToolRead {
				return true
			}
		}
	}
	for _, action := range actions {
		if policyFor(action.Type).RequiresConfirmation {
			return true
		}
	}
	return false
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
	case "create_note", "create_task", "create_book":
		if strings.TrimSpace(action.Title) == "" {
			return fmt.Errorf("%s requires title", action.Type)
		}
		if err := validateFolderValue(action.Type+" folder", action.Folder); err != nil {
			return err
		}
	case "ensure_folders":
		if len(action.Paths) == 0 {
			return fmt.Errorf("ensure_folders requires paths")
		}
		for index, path := range action.Paths {
			if err := validateFolderValue(fmt.Sprintf("ensure_folders paths[%d]", index), path); err != nil {
				return err
			}
		}
	case "link_folders", "unlink_folders":
		if len(action.Folders) < 2 {
			return fmt.Errorf("%s requires at least two folders", action.Type)
		}
		for index, folder := range action.Folders {
			if err := validateFolderValue(fmt.Sprintf("%s folders[%d]", action.Type, index), folder); err != nil {
				return err
			}
		}
	case "move_note", "update_note", "append_note", "replace_section", "mark_done", "rename_note", "duplicate_note", "trash_note", "restore_note", "archive_note", "unarchive_note", "finish_book", "update_book_metadata":
		if action.NoteID <= 0 {
			return fmt.Errorf("%s requires note_id", action.Type)
		}
		if action.Type == "move_note" || action.Type == "duplicate_note" {
			if err := validateFolderValue(action.Type+" folder", action.Folder); err != nil {
				return err
			}
		}
	case "create_folder", "folder_exists", "delete_folder", "rename_folder", "move_folder", "set_folder_colors":
		if strings.TrimSpace(action.Folder) == "" {
			return fmt.Errorf("%s requires folder", action.Type)
		}
		if err := validateFolderValue(action.Type+" folder", action.Folder); err != nil {
			return err
		}
		if action.Type == "move_folder" {
			if err := validateFolderValue("move_folder new_folder", action.NewFolder); err != nil {
				return err
			}
		}
	}
	if action.Type == "update_book_metadata" && len(action.Authors) == 0 && len(action.Genres) == 0 {
		return fmt.Errorf("update_book_metadata requires authors or genres")
	}

	switch action.Type {
	case "rename_folder":
		if strings.TrimSpace(action.NewFolder) == "" {
			return fmt.Errorf("%s requires new_folder", action.Type)
		}
	}
	if action.Type == "append_note" && strings.TrimSpace(action.Content) == "" {
		return fmt.Errorf("append_note requires content")
	}
	if action.Type == "replace_section" {
		if strings.TrimSpace(action.Section) == "" || strings.TrimSpace(action.ExpectedContent) == "" {
			return fmt.Errorf("replace_section requires section and expected_content")
		}
	}
	if action.Type == "set_graph_node_size" && (action.NodeSizeMultiplier < 0.25 || action.NodeSizeMultiplier > 3) {
		return fmt.Errorf("set_graph_node_size requires node_size_multiplier between 0.25 and 3")
	}
	return nil
}

// validateFolderValue prevents a common weak-model failure where a note file
// path is put into a folder field. CleanFolder intentionally accepts any safe
// relative path, including names ending in .md, so this semantic check belongs
// at the model-action boundary rather than in the general filesystem utility.
func validateFolderValue(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	normalized := strings.TrimRight(strings.ReplaceAll(value, "\\", "/"), "/")
	if strings.HasSuffix(strings.ToLower(normalized), ".md") {
		return fmt.Errorf("%s must be a folder path, not a note file path %q", field, value)
	}
	return nil
}
