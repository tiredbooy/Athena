package chat

import (
	"regexp"
	"strings"

	"github.com/tiredbooy/internal/models"
)

// graphIntentPattern recognizes the words users actually reach for when they
// mean the Obsidian graph. "orb" is Athena's own word for a folder's graph node
// (docs/notes/README.md), so "make the work orb better" or "make projects stand
// out" is a graph request that never contains "color" or "graph" — before this,
// such a goal matched no branch at all and fell through to the full contract,
// which is exactly the wall of text a ~2B model stops reading.
//
// Matched at a word boundary on purpose: plain substring matching would read
// "absorb" and "lifestyle" as graph intent and advertise vault mutations for a
// goal that has nothing to do with the graph. The boundary is leading-only so
// ordinary inflections still count ("orbs", "coloring", "styling").
// ponytail: "style" alone can fire on "write it in the style of X"; the cost is
// two extra advertised actions, so narrow it only if that shows up for real.
var graphIntentPattern = regexp.MustCompile(`\b(orb|graph|colou?r|styl|stand out)`)

const taskActionContractPrefix = "[ATHENA TASK ACTION CONTRACT]"

var mutationActionTypes = []string{
	"create_note", "create_task", "create_book", "update_book_metadata", "finish_book",
	"ensure_folders", "move_note", "append_note", "replace_section", "update_note", "mark_done",
	"create_folder", "delete_folder", "rename_folder", "move_folder", "link_folders", "unlink_folders",
	"create_graph_folder", "set_folder_colors", "set_graph_node_size", "rename_note", "duplicate_note", "trash_note",
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
	if graphIntentPattern.MatchString(lower) {
		// create_graph_folder is here rather than in the folder branch above: "add
		// projects to the graph" is the phrasing it exists to serve, and a folder
		// goal that never mentions the graph wants create_folder, not an orb.
		add("create_graph_folder", "set_folder_colors", "set_graph_node_size")
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

// actionPurpose is the model's only description of what an action means. It
// used to live in the system prompt as the full action catalog, which a ~2B
// model reads alongside ~180 lines of policy and then ignores. Keeping it here
// means each turn shows only the handful of actions its goal can use (R-06).
// Each line stays short on purpose: a small model follows a clause, not a
// paragraph.
var actionPurpose = map[string]string{
	"create_note":          "any user content: journals, research, lists, ideas, book notes",
	"create_task":          "only when the user wants done/undone state",
	"create_book":          "the user started, is reading, or finished a book",
	"update_book_metadata": "fill missing fields from facts the user stated; never invent them",
	"finish_book":          "the user explicitly finished a tracked book",
	"ensure_folders":       "create destination folders the user explicitly asked for",
	"move_note":            "move an existing note to another existing folder",
	"append_note":          "add to a note and keep its existing body (preferred for adding)",
	"replace_section":      "replace one heading section; expected_content must match what you read",
	"update_note":          "replace the whole body; only on an explicit full-replacement request",
	"mark_done":            "set an existing task's done state",
	"create_folder":        "create one folder the user explicitly asked for",
	"delete_folder":        "delete an empty folder",
	"rename_folder":        "rename in place; new_folder is a name, not a path",
	"move_folder":          "move a folder under an existing parent; omit new_folder for the vault root",
	"link_folders":         "connect existing folders in the Obsidian graph; creates no directories",
	"unlink_folders":       "remove an explicit graph connection; creates no directories",
	"create_graph_folder":  "add a folder to the graph: makes the folder, its index note and its orb; the parent folder must already exist",
	"set_folder_colors":    "color one folder's graph node, its orb; omit color unless the user named one",
	"set_graph_node_size":  "resize every graph orb at once; it cannot target one folder",
	"rename_note":          "give an existing note a new title",
	"duplicate_note":       "copy an existing note",
	"trash_note":           "soft delete, reversible; use this when the user says delete a note",
	"restore_note":         "undo a trash_note",
	"archive_note":         "move a note out of the way but keep it",
	"unarchive_note":       "undo an archive_note",
}

func taskActionContractMessage(actionTypes []string) models.Message {
	var out strings.Builder
	out.WriteString(taskActionContractPrefix)
	out.WriteString("\nThese are the only mutation actions that exist for this goal. Use no other action name:")
	for _, actionType := range actionTypes {
		fields, required := actionFieldNames(actionType)
		if fields == nil {
			continue
		}
		out.WriteString("\n- ")
		out.WriteString(actionType)
		if len(required) > 0 {
			out.WriteString(" requires ")
			out.WriteString(strings.Join(required, ", "))
		}
		if actionType == "update_book_metadata" {
			out.WriteString(" plus authors or genres")
		}
		if optional := optionalFieldNames(actionType, fields, required); len(optional) > 0 {
			out.WriteString("; optional ")
			out.WriteString(strings.Join(optional, ", "))
		}
		if purpose := actionPurpose[actionType]; purpose != "" {
			out.WriteString(" — ")
			out.WriteString(purpose)
		}
	}
	out.WriteString("\nDo not substitute fields between action types. A folder field is a directory path under the vault: never a note title and never ending in .md. For multiple new folders, prefer one ensure_folders action with paths. [END ATHENA TASK ACTION CONTRACT]")
	return models.Message{Role: "system", Content: out.String()}
}

// optionalFieldNames lists the fields the contract must still mention so the
// narrowed contract can replace the deleted prompt catalog outright. Without
// them a model told only "create_note requires title" writes a titled note with
// no body and no folder.
func optionalFieldNames(actionType string, fields, required []string) []string {
	covered := make(map[string]bool, len(required)+2)
	for _, field := range required {
		covered[field] = true
	}
	if actionType == "update_book_metadata" {
		// Already named by the "plus authors or genres" clause above.
		covered["authors"], covered["genres"] = true, true
	}
	optional := make([]string, 0, len(fields))
	for _, field := range fields {
		if !covered[field] {
			optional = append(optional, field)
		}
	}
	return optional
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
	fields, required := actionFieldNames(actionType)
	if fields == nil {
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
		"type": "object", "properties": properties, "required": append([]string{"type"}, required...),
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

// actionFieldNames is the single field table behind both the provider JSON
// schema and the prose contract a no-native-tools model receives, so the two
// can never advertise different fields for the same action. A nil fields slice
// means the action type is not proposable.
func actionFieldNames(actionType string) (fields, required []string) {
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
		fields, required = []string{"folder", "include_children", "color"}, append(required, "folder")
	case "create_graph_folder":
		// notes.AddFolderToGraph(folder, color): an empty color keeps the G-04
		// sibling-contrast default, so only folder is required.
		fields, required = []string{"folder", "color"}, append(required, "folder")
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
		return nil, nil
	}
	return fields, required
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
	case "color":
		return stringSchema("Optional orb color as #RRGGBB. Omit it unless the user named a specific color; Athena then picks one that contrasts with sibling folders")
	case "isbn":
		return stringSchema("Optional ISBN")
	default:
		return map[string]any{}
	}
}
