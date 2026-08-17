// Read-tool vocabulary: the tool definitions Athena offers a model and the JSON
// Schema builders they are made of. Nothing here decides or executes anything.

package chat

import (
	"strings"

	"github.com/tiredbooy/internal/models"
)

func readToolDefinitions(actionTypes ...[]string) []models.ToolDefinition {
	allowedActions := allowedProposalActionTypes(actionTypes...)
	return []models.ToolDefinition{
		toolDefinition("propose_actions", "Required for any mutation decision while this tool is available. Submit the smallest non-empty vault action plan needed for the current goal. Actions available for this task: "+strings.Join(allowedActions, ", ")+". This proposes work only; Athena validates, approves, executes, and verifies it outside the model.", objectSchema(
			map[string]any{
				"summary": stringSchema("Short user-facing description of the proposed work; do not claim it already happened"),
				"actions": map[string]any{"type": "array", "items": proposalActionSchema(allowedActions), "minItems": 1},
			}, []string{"actions"})),
		toolDefinition("request_clarification", "Ask one precise blocking question only when the missing information cannot be resolved from the supplied inventory or read tools. Do not use this merely to ask for vault contents Athena can inspect.", objectSchema(
			map[string]any{"question": stringSchema("The single concise question to show the user")}, []string{"question"})),
		toolDefinition("finish_run", "Finish the current mutation run only when the goal needs no actions or verified execution observations prove every requested change succeeded. Return the concise user-facing result; never claim unverified work.", objectSchema(
			map[string]any{"message": stringSchema("The concise final answer to show the user")}, []string{"message"})),
		toolDefinition("search_notes", "Search note content semantically. Returns matching note IDs and excerpts.", objectSchema(
			map[string]any{"query": stringSchema("What to search for"), "limit": integerSchema("Maximum results, 1-8")}, []string{"query"})),
		toolDefinition("get_note", "Read the full current content of one note by its note_id.", objectSchema(
			map[string]any{"note_id": integerSchema("The note ID from inventory or a previous search")}, []string{"note_id"})),
		toolDefinition("list_notes", "List notes, optionally limited to a folder or task status. Returns metadata, not full content.", objectSchema(
			map[string]any{"folder": stringSchema("Optional vault-relative folder"), "status": stringSchema("Optional task status: open, done, or any"), "limit": integerSchema("Maximum results, 1-8")}, nil)),
		toolDefinition("list_folders", "List the authoritative vault folder tree, including each folder's existing parent, children, and explicit graph links.", objectSchema(nil, nil)),
		toolDefinition("find_notes_by_title", "Find notes by a case-insensitive title fragment. Use this before guessing a note ID.", objectSchema(
			map[string]any{"query": stringSchema("Title words to match"), "limit": integerSchema("Maximum results, 1-8")}, []string{"query"})),
		toolDefinition("get_notes", "Read up to eight notes by ID in one call.", objectSchema(map[string]any{"note_ids": map[string]any{"type": "array", "items": integerSchema("Note ID")}}, []string{"note_ids"})),
		toolDefinition("get_note_by_path", "Read a note by its vault-relative Markdown path.", objectSchema(map[string]any{"path": stringSchema("For example projects/example.md")}, []string{"path"})),
		toolDefinition("list_tags", "List tags and their note counts.", objectSchema(nil, nil)),
		toolDefinition("get_note_links", "Get a note's direct outgoing links and backlinks.", objectSchema(map[string]any{"note_id": integerSchema("Note ID")}, []string{"note_id"})),
		toolDefinition("find_duplicate_titles", "Find potentially duplicate notes by normalized title.", objectSchema(nil, nil)),
		toolDefinition("get_daily_note", "Read a daily note at daily/YYYY-MM-DD.md; omit date for today.", objectSchema(map[string]any{"date": stringSchema("Optional ISO date YYYY-MM-DD")}, nil)),
		toolDefinition("lookup_book", "Check an exact book title against the local cache and optional Open Library catalog before creating or renaming a book. If suggested_title is returned, ask the user to confirm it; never silently replace their title.", objectSchema(
			map[string]any{"title": stringSchema("Book title exactly as the user supplied it"), "isbn": stringSchema("Optional ISBN")}, []string{"title"})),
	}
}

func toolDefinition(name, description string, parameters map[string]any) models.ToolDefinition {
	return models.ToolDefinition{Type: "function", Function: models.ToolFunction{Name: name, Description: description, Parameters: parameters}}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integerSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}
