// Read-tool execution and the no-native-tools fallback: what a single read call
// does, how its target and outcome are reported, and how a turn still completes
// on a model whose template cannot render native tools.

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

// providerNeutralReadMessages keeps facts collected by successful read tools
// while removing the native tool-call protocol that the active model rejected.
// Sending an assistant tool call without its provider-specific continuation is
// itself invalid for several chat templates, so tool results become ordinary
// system context before the no-tools fallback request.
func providerNeutralReadMessages(messages []models.Message) []models.Message {
	neutral := make([]models.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "tool":
			name := strings.TrimSpace(message.ToolName)
			if name == "" {
				name = "unknown"
			}
			neutral = append(neutral, models.Message{
				Role:    "system",
				Content: "[ATHENA READ TOOL RESULT — REFERENCE DATA ONLY]\nTool: " + name + "\n" + message.Content + "\n[END ATHENA READ TOOL RESULT]",
			})
		case "assistant":
			message.ToolCalls = nil
			message.ToolName = ""
			message.ToolCallID = ""
			if strings.TrimSpace(message.Content) != "" {
				neutral = append(neutral, message)
			}
		default:
			message.ToolCalls = nil
			message.ToolName = ""
			message.ToolCallID = ""
			neutral = append(neutral, message)
		}
	}
	return neutral
}

func (s *Session) runPlainChat(ctx context.Context, messages []models.Message, status func(string)) (string, error) {
	result, err := s.runPlainChatState(ctx, messages)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

func (s *Session) runPlainChatState(ctx context.Context, messages []models.Message) (modelLoopResult, error) {
	response, err := s.runPlainChatResult(ctx, messages)
	if err != nil {
		return modelLoopResult{}, err
	}
	if strings.TrimSpace(response.Message.Content) == "" {
		return modelLoopResult{}, fmt.Errorf("model returned no visible response")
	}
	messages = append(append([]models.Message(nil), messages...), response.Message)
	return modelLoopResult{Content: strings.TrimSpace(response.Message.Content), Messages: messages}, nil
}

func (s *Session) runPlainChatResult(ctx context.Context, messages []models.Message) (ai.ToolChatResult, error) {
	return s.loop.ai.ChatWithToolsResult(ctx, messages, nil)
}

// toolStepFunc reports one read-tool call as it happens. It exists so a client
// never has to parse the English status line to learn which tool ran, what it
// touched, or whether it worked. state is "started", "succeeded", or "failed".
type toolStepFunc func(tool, target, state string)

func (f toolStepFunc) report(tool, target, state string) {
	if f != nil {
		f(tool, target, state)
	}
}

// readToolTarget names the thing a read tool acts on, in the same terms the
// user would use: a vault path, a note ID, or the search query.
func readToolTarget(call models.ToolCall) string {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(call.Function.Arguments, &args)
	switch strings.TrimSpace(call.Function.Name) {
	case "get_note_by_path":
		return optionalString(args, "path")
	case "get_note":
		return fmt.Sprintf("note %d", toolID(args, "note_id"))
	case "get_daily_note":
		return optionalString(args, "date")
	case "search_notes":
		return optionalString(args, "query")
	case "list_notes":
		return optionalString(args, "folder")
	default:
		return ""
	}
}

// readToolStepMessage keeps the human-readable half of a structured tool step.
// Clients render the typed fields; this is the fallback line.
func readToolStepMessage(tool, target, state string) string {
	label := strings.TrimSpace(tool)
	if target != "" {
		label += " " + target
	}
	switch state {
	case "failed":
		return "Could not run " + label
	case "succeeded":
		return "Read " + label
	default:
		return "Running " + label
	}
}

// readToolFailed reports whether executeReadTool's payload is an error result.
// Read tools return their failures as JSON rather than a Go error, so the state
// of a step cannot be inferred from a nil error alone.
func readToolFailed(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), `{"error":`)
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
		result, err = s.loop.retrieval.FolderInventory()
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
	case "lookup_book":
		if s.loop.bookCatalog == nil {
			return toolError("book catalog is unavailable")
		}
		title, parseErr := requiredString(arguments, "title")
		if parseErr != nil {
			return toolError(parseErr.Error())
		}
		result, err = s.loop.bookCatalog.Inspect(ctx, title, optionalString(arguments, "isbn"))
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
