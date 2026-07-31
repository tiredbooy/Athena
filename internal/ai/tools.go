package ai

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Action is the JSON payload the model emits inside a fenced ```action
// block. Not every field applies to every Type:
//   - create_note: Title, Content, Tags
//   - create_task: Title, Content
//   - update_note: NoteID, Content
//   - mark_done:   NoteID, Done
type Action struct {
	Type    string   `json:"type"`
	NoteID  int64    `json:"note_id,omitempty"`
	Title   string   `json:"title,omitempty"`
	Content string   `json:"content,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Done    bool     `json:"done,omitempty"`
}

type ActionResult struct {
	Action  Action
	Message string
	Err     error
}

var fenceRe = regexp.MustCompile("(?s)```action\\s*(\\{.*?\\})\\s*```")

// ExtractActions pulls action blocks out of raw model output, returning
// the cleaned display text (fences stripped) plus the parsed actions.
// Malformed JSON inside a fence is silently skipped rather than failing
// the whole turn — a broken action shouldn't lose the model's reply.
func ExtractActions(raw string) (cleaned string, found []Action) {
	matches := fenceRe.FindAllStringSubmatchIndex(raw, -1)
	if len(matches) == 0 {
		return raw, nil
	}

	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(raw[last:m[0]])
		last = m[1]

		payload := raw[m[2]:m[3]]
		var a Action
		if err := json.Unmarshal([]byte(payload), &a); err == nil && a.Type != "" {
			found = append(found, a)
		}
	}
	b.WriteString(raw[last:])
	return strings.TrimSpace(b.String()), found
}
