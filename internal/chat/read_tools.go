package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/retrieval"
)

const (
	maxReadToolRounds = 4
	maxAutoContinues  = 1
	readToolBatchSize = 4
	maxReadToolLimit  = 24
	maxToolContent    = 6_000
	readToolTimeout   = 10 * time.Second
)

func readToolDefinitions() []models.ToolDefinition {
	return []models.ToolDefinition{
		toolDefinition("search_notes", "Search note content semantically. Returns matching note IDs and excerpts.", objectSchema(
			map[string]any{"query": stringSchema("What to search for"), "limit": integerSchema("Maximum results, 1-8")}, []string{"query"})),
		toolDefinition("get_note", "Read the full current content of one note by its note_id.", objectSchema(
			map[string]any{"note_id": integerSchema("The note ID from inventory or a previous search")}, []string{"note_id"})),
		toolDefinition("list_notes", "List notes, optionally limited to a folder or task status. Returns metadata, not full content.", objectSchema(
			map[string]any{"folder": stringSchema("Optional vault-relative folder"), "status": stringSchema("Optional task status: open, done, or any"), "limit": integerSchema("Maximum results, 1-8")}, nil)),
		toolDefinition("list_folders", "List the current vault folder tree.", objectSchema(nil, nil)),
		toolDefinition("find_notes_by_title", "Find notes by a case-insensitive title fragment. Use this before guessing a note ID.", objectSchema(
			map[string]any{"query": stringSchema("Title words to match"), "limit": integerSchema("Maximum results, 1-8")}, []string{"query"})),
		toolDefinition("get_notes", "Read up to eight notes by ID in one call.", objectSchema(map[string]any{"note_ids": map[string]any{"type": "array", "items": integerSchema("Note ID")}}, []string{"note_ids"})),
		toolDefinition("get_note_by_path", "Read a note by its vault-relative Markdown path.", objectSchema(map[string]any{"path": stringSchema("For example books/foundation.md")}, []string{"path"})),
		toolDefinition("list_tags", "List tags and their note counts.", objectSchema(nil, nil)),
		toolDefinition("get_note_links", "Get a note's direct outgoing links and backlinks.", objectSchema(map[string]any{"note_id": integerSchema("Note ID")}, []string{"note_id"})),
		toolDefinition("find_duplicate_titles", "Find potentially duplicate notes by normalized title.", objectSchema(nil, nil)),
		toolDefinition("get_daily_note", "Read a daily note at daily/YYYY-MM-DD.md; omit date for today.", objectSchema(map[string]any{"date": stringSchema("Optional ISO date YYYY-MM-DD")}, nil)),
	}
}

func toolDefinition(name, description string, parameters map[string]any) models.ToolDefinition {
	return models.ToolDefinition{Type: "function", Function: models.ToolFunction{Name: name, Description: description, Parameters: parameters}}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
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

func (s *Session) runReadToolLoop(ctx context.Context, messages []models.Message, status func(string)) (string, error) {
	tools := readToolDefinitions()
	continuations := 0
	var partialResponses []string
	for round := 0; round < maxReadToolRounds; round++ {
		response, err := s.chatWithRetry(ctx, messages, tools, status)
		if err != nil {
			// Some local models can emit Athena action blocks but reject Ollama's
			// native tools schema. Keep the turn useful without bypassing dispatch.
			if round == 0 && s.loop.ai.Name() == "Ollama" {
				if status != nil {
					status("Tool mode was rejected — retrying normal chat with the same model")
				}
				fallback, fallbackErr := s.loop.ai.ChatWithToolsResult(ctx, messages, nil)
				if fallbackErr == nil && strings.TrimSpace(fallback.Message.Content) != "" {
					return fallback.Message.Content, nil
				}
				if fallbackErr == nil {
					fallbackErr = fmt.Errorf("model returned no visible response")
				}
				return "", fmt.Errorf("Ollama rejected native tools, then normal chat failed: %w", fallbackErr)
			}
			return "", err
		}
		if len(response.Message.ToolCalls) == 0 {
			if strings.TrimSpace(response.Message.Content) == "" {
				return "", fmt.Errorf("model returned neither an answer nor a tool call")
			}
			if isIncompleteDoneReason(response.DoneReason) && continuations < maxAutoContinues {
				continuations++
				partialResponses = append(partialResponses, response.Message.Content)
				if status != nil {
					status("Compacting context and continuing the model response")
				}
				messages = compactContinuationMessages(messages, response.Message)
				messages = append(messages, models.Message{Role: "user", Content: "Continue the unfinished response from the saved state. Do not repeat completed work or emit duplicate actions."})
				continue
			}
			partialResponses = append(partialResponses, response.Message.Content)
			return strings.TrimSpace(strings.Join(partialResponses, "\n\n")), nil
		}
		if len(response.Message.ToolCalls) > maxReadToolLimit {
			return "", fmt.Errorf("model requested %d read tools at once; safety limit is %d", len(response.Message.ToolCalls), maxReadToolLimit)
		}

		messages = append(messages, response.Message)
		calls := response.Message.ToolCalls
		for start := 0; start < len(calls); start += readToolBatchSize {
			end := min(start+readToolBatchSize, len(calls))
			if status != nil {
				status(fmt.Sprintf("Reading vault tools %d-%d of %d", start+1, end, len(calls)))
			}
			// The queue is application-owned: every accepted call is completed
			// before Athena asks the model to plan another turn.
			for _, call := range calls[start:end] {
				if status != nil {
					status(readToolActivity(call))
				}
				content := s.executeReadTool(ctx, call)
				messages = append(messages, models.Message{Role: "tool", ToolName: call.Function.Name, ToolCallID: call.ID, Content: content})
			}
		}
	}
	// A weak model can keep requesting the same read tools forever. Do not make
	// the user retry from scratch: stop granting tools and ask for an answer
	// based on the facts Athena already supplied.
	if status != nil {
		status("Read limit reached — asking the model to finish with collected results")
	}
	messages = append(messages, models.Message{Role: "user", Content: "You have enough vault results. Answer the user now using the tool results already provided. Do not request more tools."})
	final, err := s.loop.ai.ChatWithToolsResult(ctx, messages, nil)
	if err != nil {
		return "", fmt.Errorf("finish after read limit: %w", err)
	}
	if strings.TrimSpace(final.Message.Content) == "" {
		return "", fmt.Errorf("model exceeded the %d-round read-tool limit without a final answer", maxReadToolRounds)
	}
	return final.Message.Content, nil
}

func readToolActivity(call models.ToolCall) string {
	name := strings.TrimSpace(call.Function.Name)
	var args map[string]json.RawMessage
	_ = json.Unmarshal(call.Function.Arguments, &args)
	switch name {
	case "get_note_by_path":
		return "Reading " + optionalString(args, "path")
	case "get_note":
		return fmt.Sprintf("Reading note %d", toolID(args, "note_id"))
	case "get_daily_note":
		return "Reading daily note " + optionalString(args, "date")
	case "search_notes":
		return "Searching notes for “" + optionalString(args, "query") + "”"
	default:
		return "Running vault tool: " + name
	}
}

func toolID(args map[string]json.RawMessage, key string) int64 {
	var id int64
	_ = json.Unmarshal(args[key], &id)
	return id
}

func (s *Session) chatWithRetry(ctx context.Context, messages []models.Message, tools []models.ToolDefinition, status func(string)) (ai.ToolChatResult, error) {
	if status != nil {
		status(fmt.Sprintf("%s · %s is generating a plan", s.loop.ai.Name(), shortModel(s.loop.ai.ChatModel())))
	}
	response, err := s.loop.ai.ChatWithToolsResult(ctx, messages, tools)
	if err == nil || !retryableModelError(err) || ctx.Err() != nil {
		return response, err
	}
	// A tool-schema failure from Ollama is normally model capability mismatch,
	// not a transient transport error. Return it immediately so runReadToolLoop
	// can fall back to ordinary chat while most of the turn budget remains.
	if s.loop.ai.Name() == "Ollama" && len(tools) > 0 {
		return response, err
	}
	if status != nil {
		status("Retrying the model request (1/1)")
	}
	select {
	case <-ctx.Done():
		return ai.ToolChatResult{}, ctx.Err()
	case <-time.After(250 * time.Millisecond):
	}
	return s.loop.ai.ChatWithToolsResult(ctx, messages, tools)
}

func retryableModelError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "call ollama tools") || strings.Contains(text, "status 500") || strings.Contains(text, "status 502") || strings.Contains(text, "status 503") || strings.Contains(text, "status 504")
}

func isIncompleteDoneReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(reason, "length") || strings.Contains(reason, "context")
}

func compactContinuationMessages(messages []models.Message, partial models.Message) []models.Message {
	system := models.Message{Role: "system", Content: ai.SystemPromptAt(time.Now())}
	goal := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			goal = strings.Split(messages[i].Content, "\n\nVault inventory")[0]
			break
		}
	}
	state := "Original user goal:\n" + truncateToolContent(goal) + "\n\nPartial response:\n" + truncateToolContent(partial.Content)
	return []models.Message{system, {Role: "system", Content: state}}
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
	case "get_notes":
		ids, parseErr := requiredIDs(arguments, "note_ids")
		if parseErr != nil {
			return toolError(parseErr.Error())
		}
		result, err = s.loop.retrieval.NotesByID(ids)
	case "get_note_by_path":
		path, parseErr := requiredString(arguments, "path")
		if parseErr != nil {
			return toolError(parseErr.Error())
		}
		result, err = s.loop.retrieval.NoteByRelativePath(path)
		if err == nil && result == nil {
			return toolError(fmt.Sprintf("note %q was not found", path))
		}
	case "list_tags":
		result, err = s.loop.retrieval.Tags()
	case "get_note_links":
		noteID, parseErr := requiredInt64(arguments, "note_id")
		if parseErr != nil {
			return toolError(parseErr.Error())
		}
		result, err = s.loop.retrieval.Links(noteID)
	case "find_duplicate_titles":
		result, err = s.loop.retrieval.DuplicateTitles()
	case "get_daily_note":
		date := optionalString(arguments, "date")
		if date == "" {
			date = time.Now().Format("2006-01-02")
		}
		if _, parseErr := time.Parse("2006-01-02", date); parseErr != nil {
			return toolError("date must use YYYY-MM-DD")
		}
		result, err = s.loop.retrieval.NoteByRelativePath("daily/" + date + ".md")
		if err == nil && result == nil {
			return toolError(fmt.Sprintf("daily note for %s was not found", date))
		}
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

func requiredIDs(arguments map[string]json.RawMessage, name string) ([]int64, error) {
	var ids []int64
	if err := json.Unmarshal(arguments[name], &ids); err != nil || len(ids) == 0 || len(ids) > 8 {
		return nil, fmt.Errorf("%s must contain 1 to 8 note IDs", name)
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("%s must contain positive note IDs", name)
		}
	}
	return ids, nil
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
