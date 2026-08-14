package chat

import (
	"strings"

	"github.com/tiredbooy/internal/models"
)

const taskActionContractPrefix = "[ATHENA TASK ACTION CONTRACT]"

var mutationActionTypes = []string{
	"create_note", "create_task", "create_book", "update_book_metadata", "finish_book",
	"ensure_folders", "move_note", "append_note", "replace_section", "update_note", "mark_done",
	"create_folder", "delete_folder", "rename_folder", "move_folder", "link_folders", "unlink_folders",
	"set_folder_colors", "set_graph_node_size", "rename_note", "duplicate_note", "trash_note",
	"restore_note", "archive_note", "unarchive_note",
}

// actionTypesForGoal narrows the model's mutation vocabulary while remaining
// conservative. Unknown goals receive the full contract; recognized domains
// receive the core operations that can complete a compound request.
func actionTypesForGoal(goal string) []string {
	lower := strings.ToLower(goal)
	selected := make(map[string]bool)
	add := func(types ...string) {
		for _, actionType := range types {
			selected[actionType] = true
		}
	}

	if containsAny(lower, []string{"book", "reading", "author", "genre", "isbn"}) {
		add("create_book", "update_book_metadata", "finish_book", "move_note", "ensure_folders", "rename_note")
	}
	if containsAny(lower, []string{"organize", "organise", "folder", "directory", "genre"}) {
		add("ensure_folders", "create_folder", "move_note", "rename_folder", "move_folder", "set_folder_colors")
	}
	if containsAny(lower, []string{"link", "connect"}) {
		add("link_folders")
	}
	if containsAny(lower, []string{"unlink", "disconnect"}) {
		add("unlink_folders")
	}
	if containsAny(lower, []string{"task", "done", "complete"}) {
		add("create_task", "mark_done", "ensure_folders")
	}
	if containsAny(lower, []string{"note", "journal", "idea", "file"}) {
		add("create_note", "move_note", "append_note", "replace_section", "update_note", "rename_note", "duplicate_note", "ensure_folders")
	}
	if containsAny(lower, []string{"delete", "remove", "trash"}) {
		add("trash_note", "delete_folder")
	}
	if strings.Contains(lower, "restore") {
		add("restore_note")
	}
	if strings.Contains(lower, "archive") {
		add("archive_note", "unarchive_note")
	}
	if containsAny(lower, []string{"color", "colour", "graph"}) {
		add("set_folder_colors", "set_graph_node_size")
	}
	if len(selected) == 0 {
		return append([]string(nil), mutationActionTypes...)
	}
	ordered := make([]string, 0, len(selected))
	for _, actionType := range mutationActionTypes {
		if selected[actionType] {
			ordered = append(ordered, actionType)
		}
	}
	return ordered
}

func allowedProposalActionTypes(requested ...[]string) []string {
	if len(requested) == 0 || len(requested[0]) == 0 {
		return append([]string(nil), mutationActionTypes...)
	}
	known := make(map[string]bool, len(mutationActionTypes))
	for _, actionType := range mutationActionTypes {
		known[actionType] = true
	}
	allowed := make([]string, 0, len(requested[0]))
	seen := make(map[string]bool)
	for _, actionType := range requested[0] {
		if known[actionType] && !seen[actionType] {
			seen[actionType] = true
			allowed = append(allowed, actionType)
		}
	}
	if len(allowed) == 0 {
		return append([]string(nil), mutationActionTypes...)
	}
	return allowed
}

func taskActionContractMessage(actionTypes []string) models.Message {
	var out strings.Builder
	out.WriteString(taskActionContractPrefix)
	out.WriteString("\nFor this active goal, propose only these mutation actions with their required fields:")
	for _, actionType := range actionTypes {
		schema := typedActionSchema(actionType)
		if schema == nil {
			continue
		}
		required, _ := schema["required"].([]string)
		fields := make([]string, 0, len(required))
		for _, field := range required {
			if field != "type" {
				fields = append(fields, field)
			}
		}
		out.WriteString("\n- ")
		out.WriteString(actionType)
		if len(fields) > 0 {
			out.WriteString(" requires ")
			out.WriteString(strings.Join(fields, ", "))
		}
		if actionType == "update_book_metadata" {
			out.WriteString(" plus authors or genres")
		}
	}
	out.WriteString("\nDo not substitute fields between action types. For multiple new folders, prefer one ensure_folders action with paths. [END ATHENA TASK ACTION CONTRACT]")
	return models.Message{Role: "system", Content: out.String()}
}

func hasTaskActionContract(messages []models.Message) bool {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.HasPrefix(messages[index].Content, taskActionContractPrefix) {
			return true
		}
	}
	return false
}

func proposalActionSchema(actionTypes []string) map[string]any {
	variants := make([]any, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		if schema := typedActionSchema(actionType); schema != nil {
			variants = append(variants, schema)
		}
	}
	return map[string]any{"oneOf": variants}
}

func typedActionSchema(actionType string) map[string]any {
	fields := []string{}
	required := []string{"type"}
	switch actionType {
	case "create_note":
		fields, required = []string{"title", "content", "tags", "folder"}, append(required, "title")
	case "create_task":
		fields, required = []string{"title", "content", "folder"}, append(required, "title")
	case "create_book":
		fields, required = []string{"title", "folder", "isbn", "authors", "genres"}, append(required, "title")
	case "update_book_metadata":
		fields, required = []string{"note_id", "authors", "genres"}, append(required, "note_id")
	case "finish_book", "trash_note", "restore_note", "archive_note", "unarchive_note":
		fields, required = []string{"note_id"}, append(required, "note_id")
	case "ensure_folders":
		fields, required = []string{"paths"}, append(required, "paths")
	case "move_note":
		fields, required = []string{"note_id", "folder"}, append(required, "note_id", "folder")
	case "append_note", "update_note":
		fields, required = []string{"note_id", "content"}, append(required, "note_id", "content")
	case "replace_section":
		fields, required = []string{"note_id", "section", "expected_content", "content"}, append(required, "note_id", "section", "expected_content", "content")
	case "mark_done":
		fields, required = []string{"note_id", "done"}, append(required, "note_id", "done")
	case "create_folder", "delete_folder":
		fields, required = []string{"folder"}, append(required, "folder")
	case "set_folder_colors":
		fields, required = []string{"folder", "include_children"}, append(required, "folder")
	case "rename_folder":
		fields, required = []string{"folder", "new_folder"}, append(required, "folder", "new_folder")
	case "move_folder":
		fields, required = []string{"folder", "new_folder"}, append(required, "folder")
	case "link_folders", "unlink_folders":
		fields, required = []string{"folders"}, append(required, "folders")
	case "set_graph_node_size":
		fields, required = []string{"node_size_multiplier"}, append(required, "node_size_multiplier")
	case "rename_note":
		fields, required = []string{"note_id", "title"}, append(required, "note_id", "title")
	case "duplicate_note":
		fields, required = []string{"note_id", "title", "folder"}, append(required, "note_id")
	default:
		return nil
	}

	properties := map[string]any{
		"id":         stringSchema("Optional unique ID within this action batch"),
		"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"type":       map[string]any{"type": "string", "enum": []string{actionType}},
	}
	for _, field := range fields {
		properties[field] = actionFieldSchema(field)
	}
	schema := map[string]any{
		"type": "object", "properties": properties, "required": required,
		"additionalProperties": false,
	}
	if actionType == "update_book_metadata" {
		schema["anyOf"] = []any{
			map[string]any{"required": []string{"authors"}},
			map[string]any{"required": []string{"genres"}},
		}
	}
	return schema
}

func actionFieldSchema(field string) map[string]any {
	switch field {
	case "note_id":
		return integerSchema("Existing note ID resolved from inventory or read tools")
	case "title":
		return stringSchema("Exact note or book title")
	case "content":
		return stringSchema("Markdown content")
	case "section":
		return stringSchema("Markdown section heading")
	case "expected_content":
		return stringSchema("Exact previously-read section body")
	case "folder":
		return stringSchema("Vault-relative folder path")
	case "new_folder":
		return stringSchema("New folder name or destination parent")
	case "tags", "authors", "genres", "paths", "folders":
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	case "done", "include_children":
		return map[string]any{"type": "boolean"}
	case "node_size_multiplier":
		return map[string]any{"type": "number", "minimum": 0.25, "maximum": 3}
	case "isbn":
		return stringSchema("Optional ISBN")
	default:
		return map[string]any{}
	}
}
