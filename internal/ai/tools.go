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
//   - mark_done:        NoteID, Done
//   - create_folder:    Folder
//   - list_folders:     (none)
//   - folder_exists:    Folder
//   - delete_folder:    Folder
//   - rename_folder:    Folder (old), NewFolder (new name, single segment)
//   - move_folder:      Folder (old), NewFolder (new parent)
//   - rename_note:      NoteID, Title (new title)
//   - duplicate_note:   NoteID, Title (optional new title), Folder (optional target)
//   - trash_note:       NoteID
//   - restore_note:     NoteID
//   - archive_note:      NoteID
//   - unarchive_note:   NoteID
type Action struct {
	Type      string   `json:"type"`
	NoteID    int64    `json:"note_id,omitempty"`
	Title     string   `json:"title,omitempty"`
	Content   string   `json:"content,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Folder    string   `json:"folder,omitempty"`
	NewFolder string   `json:"new_folder,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Done      bool     `json:"done,omitempty"`
}

type ActionResult struct {
	Action  Action
	Message string
	Err     error
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

		payload, consumed, ok := takeJSONObject(after)
		if !ok {
			// No parseable object — keep the fence text as-is so the user
			// still sees what the model wrote.
			b.WriteString(rest[idx:])
			break
		}

		var a Action
		if err := json.Unmarshal([]byte(payload), &a); err == nil && a.Type != "" {
			found = append(found, a)
		}

		// Skip trailing whitespace and an optional closing fence.
		tail := after[consumed:]
		tail = strings.TrimLeft(tail, " \t\r\n")
		if strings.HasPrefix(tail, "```") {
			tail = tail[3:]
		}
		rest = tail
	}

	return strings.TrimSpace(b.String()), found
}

// takeJSONObject finds the first '{' and returns the brace-balanced slice
// that follows, respecting string escapes so "}" inside a string doesn't
// end the object early. consumed is the byte offset in s past the object.
func takeJSONObject(s string) (obj string, consumed int, ok bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", 0, false
	}

	depth := 0
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
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], i + 1, true
			}
		}
	}
	return "", 0, false
}
