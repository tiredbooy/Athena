package chat

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

const creativeTitleTemperature = 0.75

// refineNoteTitles gives create_note actions a small presentation-only pass.
// Explicit titles supplied by the user are always preserved. A title failure
// never blocks the note: Athena keeps the planner's original title instead.
func (s *Session) refineNoteTitles(ctx context.Context, input string, actions []ai.Action, status func(string)) []ai.Action {
	provider, ok := s.loop.ai.(ai.CreativeTextProvider)
	if !ok {
		return actions
	}
	refined := append([]ai.Action(nil), actions...)
	for index, action := range refined {
		if action.Type != "create_note" || strings.TrimSpace(action.Title) == "" || explicitTitle(input, action.Title) {
			continue
		}
		if status != nil {
			status("Choosing a more natural note title")
		}
		titleCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		generated, err := provider.CreativeText(titleCtx, []models.Message{
			{Role: "system", Content: "You create concise, specific, mildly creative titles for personal notes. Return only the title, with no quotes, explanation, date, Markdown, or prefix. Use 3 to 8 natural words. Do not invent facts."},
			{Role: "user", Content: "User request:\n" + input + "\n\nDraft title:\n" + action.Title + "\n\nNote content:\n" + truncateTitleSource(action.Content)},
		}, creativeTitleTemperature)
		cancel()
		if err != nil {
			continue
		}
		if title := cleanGeneratedTitle(generated); title != "" {
			refined[index].Title = title
		}
	}
	return refined
}

func explicitTitle(input, draft string) bool {
	input = strings.ToLower(input)
	draft = strings.ToLower(strings.TrimSpace(draft))
	if draft != "" && strings.Contains(input, draft) {
		return true
	}
	return containsAny(input, []string{"title ", "titled ", "named ", "called ", "call it ", "name it "})
}

func truncateTitleSource(content string) string {
	content = strings.TrimSpace(content)
	if utf8.RuneCountInString(content) <= 1800 {
		return content
	}
	runes := []rune(content)
	return string(runes[:1800]) + "…"
}

func cleanGeneratedTitle(value string) string {
	value = strings.TrimSpace(value)
	if newline := strings.IndexAny(value, "\r\n"); newline >= 0 {
		value = value[:newline]
	}
	value = strings.TrimSpace(strings.Trim(value, "`*_\"'"))
	for _, prefix := range []string{"title:", "title -", "title —"} {
		if strings.HasPrefix(strings.ToLower(value), prefix) {
			value = strings.TrimSpace(value[len(prefix):])
			break
		}
	}
	value = strings.Trim(value, "`*_\"'")
	if value == "" || utf8.RuneCountInString(value) > 100 || strings.ContainsAny(value, "/\\") {
		return ""
	}
	words := strings.Fields(value)
	if len(words) < 2 || len(words) > 12 {
		return ""
	}
	return value
}
