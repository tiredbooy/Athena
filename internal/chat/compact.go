package chat

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/tiredbooy/internal/agent"
	"github.com/tiredbooy/internal/models"
)

const (
	historyCompactThreshold = 12_000
	historyRecentMessages   = 6
	historySummaryItemLimit = 600

	conversationMemoryHeading = "Conversation memory (older turns):\n"
	retainedStateHeading      = "[ATHENA RETAINED STATE — APPLICATION DATA]"
)

// compactHistory retains the recent exchange verbatim and turns older turns
// into a bounded factual memory. It is deliberately deterministic: a weak
// model should not be asked to summarize its own state during recovery.
//
// The summary is lossy by design — it truncates each older turn — so anything
// needed to finish the active goal must not depend on it. Those facts are
// restated from live session state instead (M-02).
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
	summary.WriteString(conversationMemoryHeading)
	for _, message := range s.history[1:start] {
		if isRetainedState(message) {
			continue
		}
		summary.WriteString(message.Role)
		summary.WriteString(": ")
		summary.WriteString(compactText(message.Content, historySummaryItemLimit))
		summary.WriteByte('\n')
	}
	compacted := []models.Message{s.history[0]}
	if summary.Len() > len(conversationMemoryHeading) {
		compacted = append(compacted, models.Message{Role: "system", Content: strings.TrimSpace(summary.String())})
	}
	if retained, ok := s.retainedStateMessage(); ok {
		compacted = append(compacted, retained)
	}
	for _, message := range s.history[start:] {
		if isRetainedState(message) {
			continue
		}
		compacted = append(compacted, message)
	}
	s.history = compacted
	return true
}

// retainedStateMessage restates the facts the application owns, in full, on
// every compaction. The active goal, the question Athena is waiting on, the
// answers already given, and the last verified actions are structured session
// state — recovering them from a truncated paraphrase of older prose is exactly
// the failure this compaction is supposed to prevent, and a ~2B model has no
// chance of doing it reliably.
//
// The previous block is dropped before this one is written, so the retained
// state never accumulates or contradicts itself. The caller must hold mu.
func (s *Session) retainedStateMessage() (models.Message, bool) {
	if s.pendingTask == nil && len(s.lastLedger) == 0 {
		return models.Message{}, false
	}
	state := struct {
		ActiveGoal      string               `json:"active_goal,omitempty"`
		PendingQuestion string               `json:"pending_question,omitempty"`
		Answers         []string             `json:"answers,omitempty"`
		VerifiedActions []agent.LedgerRecord `json:"verified_actions,omitempty"`
	}{VerifiedActions: s.lastLedger}
	if s.pendingTask != nil {
		state.ActiveGoal = s.pendingTask.OriginalGoal
		state.PendingQuestion = s.pendingTask.Question
		state.Answers = s.pendingTask.Answers
	}
	// The fields are strings, slices of strings, and a slice of plain structs,
	// so encoding cannot fail.
	raw, _ := json.Marshal(state)
	return models.Message{Role: "system", Content: retainedStateHeading + "\n" + string(raw) + `
[END ATHENA RETAINED STATE]
These are Athena's own records and survive compaction. Continue the active goal from them; the conversation memory above is abbreviated and must not be used to contradict them. Never repeat an action listed as succeeded.`}, true
}

func isRetainedState(message models.Message) bool {
	return strings.HasPrefix(message.Content, retainedStateHeading)
}

func compactText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	// The limit is a byte budget for the prompt, but the vault is personal notes
	// full of accents, CJK and emoji. Cutting at an arbitrary byte offset splits a
	// multi-byte rune and feeds the model a replacement character, so back off to
	// the nearest rune boundary at or before the limit (at most three bytes).
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit] + "…"
}
