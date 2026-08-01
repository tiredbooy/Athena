package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/retrieval"
)

const (
	maxReadToolRounds = 4
	maxReadToolCalls  = 4
	maxReadToolLimit  = 8
	maxToolContent    = 6_000
	readToolTimeout   = 10 * time.Second
)

func readToolDefinitions() []models.ToolDefinition {
	return []models.ToolDefinition{
		toolDefinition("search_notes", "Search note content semantically. Returns matching note IDs and excerpts.", objectSchema(
			map[string]any{"query": stringSchema("What to search for"), "limit": integerSchema("Maximum results, 1-8")}, "query")),
		toolDefinition("get_note", "Read the full current content of one note by its note_id.", objectSchema(
			map[string]any{"note_id": integerSchema("The note ID from inventory or a previous search")}, "note_id")),
		toolDefinition("list_notes", "List notes, optionally limited to a folder or task status. Returns metadata, not full content.", objectSchema(
			map[string]any{"folder": stringSchema("Optional vault-relative folder"), "status": stringSchema("Optional task status: open, done, or any"), "limit": integerSchema("Maximum results, 1-8")}, nil)),
		toolDefinition("list_folders", "List the current vault folder tree.", objectSchema(nil, nil)),
		toolDefinition("find_notes_by_title", "Find notes by a case-insensitive title fragment. Use this before guessing a note ID.", objectSchema(
			map[string]any{"query": stringSchema("Title words to match"), "limit": integerSchema("Maximum results, 1-8")}, "query")),
	}
}

func toolDefinition(name, description string, parameters map[string]any) models.ToolDefinition {
	return models.ToolDefinition{Type: "function", Function: models.ToolFunction{Name: name, Description: description, Parameters: parameters}}
}

func objectSchema(properties map[string]any, required any) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if required != nil {
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

func (s *Session) runReadToolLoop(ctx context.Context, messages []models.Message, status func(string)) (string, error) {
	tools := readToolDefinitions()
	for round := 0; round < maxReadToolRounds; round++ {
		if status != nil {
			status(fmt.Sprintf("Planning with %s", s.loop.ai.ChatModel()))
		}
		response, err := s.loop.ai.ChatWithTools(ctx, messages, tools)
		if err != nil {
			return "", err
		}
		if len(response.ToolCalls) == 0 {
			if strings.TrimSpace(response.Content) == "" {
				return "", fmt.Errorf("model returned neither an answer nor a tool call")
			}
			return response.Content, nil
		}
		if len(response.ToolCalls) > maxReadToolCalls {
			return "", fmt.Errorf("model requested %d read tools at once; limit is %d", len(response.ToolCalls), maxReadToolCalls)
		}

		messages = append(messages, response)
		if status != nil {
			status(fmt.Sprintf("Reading vault with %d tool request(s)", len(response.ToolCalls)))
		}
		for _, call := range response.ToolCalls {
			content := s.executeReadTool(ctx, call)
			messages = append(messages, models.Message{Role: "tool", ToolName: call.Function.Name, Content: content})
		}
	}
	return "", fmt.Errorf("model exceeded the %d-round read-tool limit", maxReadToolRounds)
}

func (s *Session) executeReadTool(ctx context.Context, call models.ToolCall) string {
	ctx, cancel := context.WithTimeout(ctx, readToolTimeout)
	defer cancel()

	name := strings.TrimSpace(call.Function.Name)
	var arguments map[string]json.RawMessage
	if len(call.Function.Arguments) == 0 || json.Unmarshal(call.Function.Arguments, &arguments) != nil {
		return toolError("arguments must be a JSON object")
	}

	limit := toolLimit(arguments)
	var result any
	var err error
	switch name {
	case "search_notes":
		query, parseErr := requiredString(arguments, "query")
		if parseErr != nil {
			return toolError(parseErr.Error())
		}
		result, err = s.loop.retrieval.SearchNotes(ctx, query, limit)
	case "get_note":
		noteID, parseErr := requiredInt64(arguments, "note_id")
		if parseErr != nil {
			return toolError(parseErr.Error())
		}
		result, err = s.loop.retrieval.NoteByID(noteID)
		if err == nil && result == nil {
			return toolError(fmt.Sprintf("note %d was not found", noteID))
		}
	case "list_notes":
		result, err = filterCatalog(s.loop.retrieval, optionalString(arguments, "folder"), optionalString(arguments, "status"), limit)
	case "list_folders":
		result, err = s.loop.retrieval.Folders()
	case "find_notes_by_title":
		query, parseErr := requiredString(arguments, "query")
		if parseErr != nil {
			return toolError(parseErr.Error())
		}
		result, err = s.loop.retrieval.FindNotesByTitle(query, limit)
	default:
		return toolError(fmt.Sprintf("unknown read tool %q", name))
	}
	if err != nil {
		return toolError(err.Error())
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return toolError(fmt.Sprintf("encode result: %v", err))
	}
	return truncateToolContent(string(encoded))
}

func filterCatalog(service *retrieval.Service, folder, status string, limit int) ([]retrieval.CatalogEntry, error) {
	catalog, err := service.Inventory()
	if err != nil {
		return nil, err
	}
	folder = strings.Trim(strings.TrimSpace(folder), "/")
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && status != "any" && status != "open" && status != "done" {
		return nil, fmt.Errorf("status must be open, done, or any")
	}
	filtered := make([]retrieval.CatalogEntry, 0, limit)
	for _, entry := range catalog {
		if folder != "" && entry.Folder != folder {
			continue
		}
		if status == "open" && (entry.Type != "task" || entry.Done) {
			continue
		}
		if status == "done" && (entry.Type != "task" || !entry.Done) {
			continue
		}
		filtered = append(filtered, entry)
		if len(filtered) == limit {
			break
		}
	}
	return filtered, nil
}

func requiredString(arguments map[string]json.RawMessage, name string) (string, error) {
	value := optionalString(arguments, name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func optionalString(arguments map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(arguments[name], &value)
	return strings.TrimSpace(value)
}

func requiredInt64(arguments map[string]json.RawMessage, name string) (int64, error) {
	var value int64
	if err := json.Unmarshal(arguments[name], &value); err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func toolLimit(arguments map[string]json.RawMessage) int {
	var limit int
	if json.Unmarshal(arguments["limit"], &limit) != nil || limit < 1 {
		return maxReadToolLimit
	}
	if limit > maxReadToolLimit {
		return maxReadToolLimit
	}
	return limit
}

func toolError(message string) string {
	return `{"error":` + strconvQuote(message) + `}`
}

func truncateToolContent(content string) string {
	if len(content) <= maxToolContent {
		return content
	}
	return content[:maxToolContent] + `…(truncated)`
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
