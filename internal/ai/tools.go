package ai

import (
	"encoding/json"
	"strings"
)

// Action is the JSON payload the model emits inside a fenced ```action
// block. Not every field applies to every Type:
//   - create_note:      Title, Content, Tags, Folder
//   - create_task:      Title, Content, Folder
//   - ensure_folders:   Paths
//   - move_note:        NoteID, Folder
//   - update_note:      NoteID, Content
//   - append_note:      NoteID, Content
//   - replace_section:  NoteID, Section, Content, ExpectedContent
//   - mark_done:        NoteID, Done
//   - create_folder:    Folder
//   - list_folders:     (none)
//   - folder_exists:    Folder
//   - delete_folder:    Folder
//   - rename_folder:    Folder (old), NewFolder (new name, single segment)
//   - move_folder:      Folder (old), NewFolder (new parent)
//   - link_folders:     Folders
//   - unlink_folders:   Folders
//   - set_folder_colors: Folder, IncludeChildren (optional)
//   - set_graph_node_size: NodeSizeMultiplier
//   - rename_note:      NoteID, Title (new title)
//   - duplicate_note:   NoteID, Title (optional new title), Folder (optional target)
//   - trash_note:       NoteID
//   - restore_note:     NoteID
//   - archive_note:      NoteID
//   - unarchive_note:   NoteID
//   - create_book:      Title, Folder (optional), ISBN (optional)
//   - finish_book:      NoteID
type Action struct {
	// ID identifies an action within a batch plan. It is optional for
	// single/legacy actions; a batch runs concurrently only when every action
	// has a unique ID.
	ID string `json:"id,omitempty"`
	// DependsOn names actions that must succeed before this one can run.
	DependsOn []string `json:"depends_on,omitempty"`
	Type      string   `json:"type"`
	NoteID    int64    `json:"note_id,omitempty"`
	Title     string   `json:"title,omitempty"`
	Content   string   `json:"content,omitempty"`
	// Section and ExpectedContent make a targeted edit conditional on the text
	// the model actually read, preventing stale plans from overwriting a newer
	// manual edit.
	Section         string   `json:"section,omitempty"`
	ExpectedContent string   `json:"expected_content,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Folder          string   `json:"folder,omitempty"`
	// IncludeChildren applies a folder visual setting to direct child folders
	// as well. It is deliberately not recursive.
	IncludeChildren bool `json:"include_children,omitempty"`
	// NodeSizeMultiplier is Obsidian's global graph node-size multiplier.
	// Obsidian does not support a different native size for each color group.
	NodeSizeMultiplier float64 `json:"node_size_multiplier,omitempty"`
	// Path is accepted from weaker models that use a generic path field for a
	// folder action. normalizeAction maps it into Folder before dispatch.
	Path      string   `json:"path,omitempty"`
	NewFolder string   `json:"new_folder,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Folders   []string `json:"folders,omitempty"`
	Done      bool     `json:"done,omitempty"`
	ISBN      string   `json:"isbn,omitempty"`
}

type ActionResult struct {
	Action  Action `json:"action"`
	Message string `json:"message,omitempty"`
	// Error is the transport-safe form of Err for a future UI protocol.
	Error string `json:"error,omitempty"`
	Err   error  `json:"-"`
}

// ExtractActions pulls action blocks out of raw model output, returning
// the cleaned display text (fences stripped) plus the parsed actions.
//
// Tolerates the formats small local models actually produce:
//   - properly closed ```action ... ``` fences
//   - unclosed fences (model stops after the JSON object)
//   - brace-balanced JSON (not non-greedy regex, which breaks on "}" in strings)
//
// Malformed JSON inside a fence is silently skipped rather than failing
// the whole turn — a broken action shouldn't lose the model's reply.
func ExtractActions(raw string) (cleaned string, found []Action) {
	const open = "```action"

	var b strings.Builder
	rest := raw
	for {
		idx := strings.Index(rest, open)
		if idx < 0 {
			b.WriteString(rest)
			break
		}

		b.WriteString(rest[:idx])
		after := rest[idx+len(open):]

		// Optional language tag whitespace / newline after the fence opener.
		after = strings.TrimLeft(after, " \t\r\n")

		payload, consumed, ok := takeJSONValue(after)
		if !ok {
			// No parseable object — keep the fence text as-is so the user
			// still sees what the model wrote.
			b.WriteString(rest[idx:])
			break
		}

		found = append(found, decodeActions(payload)...)

		// Skip trailing whitespace and an optional closing fence.
		tail := after[consumed:]
		tail = strings.TrimLeft(tail, " \t\r\n")
		if strings.HasPrefix(tail, "```") {
			tail = tail[3:]
		}
		rest = tail
	}

	cleaned = strings.TrimSpace(b.String())
	if len(found) == 0 {
		// Some small models omit the action fence but return a standalone JSON
		// object or array. Accept only a payload that occupies the entire reply;
		// never scrape arbitrary prose or note content for executable JSON.
		if payload, consumed, ok := takeJSONValue(cleaned); ok && strings.TrimSpace(cleaned[consumed:]) == "" {
			if actions := decodeActions(payload); len(actions) > 0 {
				return "", actions
			}
		}
	}
	return cleaned, found
}

// takeJSONValue finds the first JSON object or array and returns the balanced
// value. Small models commonly emit either one action object, an array of
// actions, or an {"actions":[...]} envelope inside an action fence.
func takeJSONValue(s string) (value string, consumed int, ok bool) {
	objectStart := strings.IndexByte(s, '{')
	arrayStart := strings.IndexByte(s, '[')
	start := objectStart
	if start < 0 || (arrayStart >= 0 && arrayStart < start) {
		start = arrayStart
	}
	if start < 0 {
		return "", 0, false
	}

	stack := make([]byte, 0, 4)
	inString := false
	escape := false

	for i := start; i < len(s); i++ {
		c := s[i]

		if inString {
			if escape {
				escape = false
				continue
			}
			switch c {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}':
			if len(stack) == 0 || stack[len(stack)-1] != '}' {
				return "", 0, false
			}
			stack = stack[:len(stack)-1]
		case ']':
			if len(stack) == 0 || stack[len(stack)-1] != ']' {
				return "", 0, false
			}
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			return s[start : i+1], i + 1, true
		}
	}
	return "", 0, false
}

func decodeActions(payload string) []Action {
	if strings.HasPrefix(strings.TrimSpace(payload), "[") {
		var actions []Action
		if err := json.Unmarshal([]byte(payload), &actions); err != nil {
			return nil
		}
		return validActions(actions)
	}

	var action Action
	if err := json.Unmarshal([]byte(payload), &action); err != nil {
		return nil
	}
	if action.Type != "" {
		return validActions([]Action{action})
	}

	var envelope struct {
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return nil
	}
	return validActions(envelope.Actions)
}

func validActions(actions []Action) []Action {
	valid := actions[:0]
	for _, action := range actions {
		action = normalizeAction(action)
		if action.Type != "" {
			valid = append(valid, action)
		}
	}
	return valid
}

func normalizeAction(action Action) Action {
	if action.Folder == "" {
		action.Folder = action.Path
	}
	switch strings.ToLower(strings.ReplaceAll(action.Type, "-", "_")) {
	case "createfolder", "make_folder", "makefolder":
		action.Type = "create_folder"
	case "deletefolder", "remove_folder", "removefolder":
		action.Type = "delete_folder"
	case "ensurefolders":
		action.Type = "ensure_folders"
	case "movenote":
		action.Type = "move_note"
	case "movefolder":
		action.Type = "move_folder"
	case "unlinkfolders", "unlink_folder", "disconnect_folders", "disconnectfolders":
		action.Type = "unlink_folders"
	case "renamefolder":
		action.Type = "rename_folder"
	case "color_folder", "colorfolders", "setfoldercolors", "add_folder_color", "addfoldercolor":
		action.Type = "set_folder_colors"
	case "set_graph_size", "setgraphnodesize", "resize_graph_nodes", "resizegraphnodes":
		action.Type = "set_graph_node_size"
	case "createnote":
		action.Type = "create_note"
	case "createtask":
		action.Type = "create_task"
	}
	return action
}
