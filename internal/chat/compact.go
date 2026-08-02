package chat

import (
	"strings"

	"github.com/tiredbooy/internal/models"
)

const (
	historyCompactThreshold = 12_000
	historyRecentMessages   = 6
	historySummaryItemLimit = 600
)

// compactHistory retains the recent exchange verbatim and turns older turns
// into a bounded factual memory. It is deliberately deterministic: a weak
// model should not be asked to summarize its own state during recovery.
func (s *Session) compactHistory(force bool) bool {
	if len(s.history) <= 1 {
		return false
	}
	var size int
	for _, message := range s.history {
		size += len(message.Content)
	}
	if !force && size < historyCompactThreshold {
		return false
	}
	start := len(s.history) - historyRecentMessages
	if start < 1 {
		start = 1
	}
	if start == 1 {
		return false
	}

	var summary strings.Builder
	summary.WriteString("Conversation memory (older turns):\n")
	for _, message := range s.history[1:start] {
		summary.WriteString(message.Role)
		summary.WriteString(": ")
		summary.WriteString(compactText(message.Content, historySummaryItemLimit))
		summary.WriteByte('\n')
	}
	compacted := []models.Message{s.history[0]}
	if summary.Len() > len("Conversation memory (older turns):\n") {
		compacted = append(compacted, models.Message{Role: "system", Content: strings.TrimSpace(summary.String())})
	}
	compacted = append(compacted, s.history[start:]...)
	s.history = compacted
	return true
}

func compactText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}
