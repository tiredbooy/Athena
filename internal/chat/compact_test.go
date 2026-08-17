package chat

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tiredbooy/internal/agent"
	"github.com/tiredbooy/internal/models"
)

func TestCompactHistoryKeepsSystemAndRecentTurns(t *testing.T) {
	session := &Session{history: []models.Message{{Role: "system", Content: "rules"}}}
	for i := 0; i < 8; i++ {
		session.history = append(session.history, models.Message{Role: "user", Content: strings.Repeat("x", 100)})
	}
	if !session.compactHistory(true) {
		t.Fatal("expected forced compaction")
	}
	if session.history[0].Role != "system" || session.history[0].Content != "rules" {
		t.Fatalf("system message changed: %+v", session.history[0])
	}
	if !strings.Contains(session.history[1].Content, "Conversation memory") {
		t.Fatalf("summary missing: %+v", session.history)
	}
	if len(session.history) != 8 { // system + summary + six most recent messages
		t.Fatalf("history length = %d, want 8", len(session.history))
	}
}

// M-02: the goal, the open question, the answers already given, and the last
// verified actions are application-owned facts. Compaction must restate them,
// not hope they survived inside a truncated paraphrase of older prose.
func TestCompactionRetainsTheActiveGoalAnswersAndVerifiedActions(t *testing.T) {
	session := &Session{
		history: []models.Message{{Role: "system", Content: "rules"}},
		pendingTask: &PendingTask{
			OriginalGoal: "organize my books into genre folders",
			Question:     "Which genre should Project Hail Mary use?",
			Answers:      []string{"Science Fiction"},
		},
		lastLedger: []agent.LedgerRecord{{
			Action: "ensure_folders", Target: "books/reading/Science Fiction",
			Status: "succeeded", Message: "created books/reading/Science Fiction",
		}},
	}
	// Filler turns that mention none of the above, long enough that the summary
	// truncates them. Only the retained state can carry the goal now.
	for i := 0; i < 8; i++ {
		session.history = append(session.history, models.Message{Role: "user", Content: strings.Repeat("unrelated chatter ", 60)})
	}

	if !session.compactHistory(true) {
		t.Fatal("expected forced compaction")
	}
	retained := retainedStateContent(session)
	if retained == "" {
		t.Fatalf("compaction kept no application-owned state: %+v", session.history)
	}
	for _, want := range []string{
		"organize my books into genre folders",
		"Which genre should Project Hail Mary use?",
		"Science Fiction",
		"ensure_folders",
		"succeeded",
	} {
		if !strings.Contains(retained, want) {
			t.Fatalf("retained state is missing %q: %s", want, retained)
		}
	}

	// A second compaction rebuilds the block from live state instead of
	// stacking a stale copy next to it.
	for i := 0; i < 6; i++ {
		session.history = append(session.history, models.Message{Role: "user", Content: strings.Repeat("more chatter ", 60)})
	}
	if !session.compactHistory(true) {
		t.Fatal("expected a second forced compaction")
	}
	var blocks int
	for _, message := range session.history {
		if isRetainedState(message) {
			blocks++
		}
	}
	if blocks != 1 {
		t.Fatalf("history holds %d retained-state blocks, want exactly 1", blocks)
	}
}

func retainedStateContent(session *Session) string {
	for _, message := range session.history {
		if isRetainedState(message) {
			return message.Content
		}
	}
	return ""
}

// The vault is personal notes: Persian, CJK and emoji are ordinary content, and
// compacted text is fed back to the model as memory. Truncating on a byte index
// splits a multi-byte rune and hands the model a replacement character.
func TestCompactTextTruncatesOnRuneBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"persian", strings.Repeat("سلام دنیا ", 40)},   // 2-byte runes
		{"cjk", strings.Repeat("知識のグラフ ", 40)},          // 3-byte runes
		{"emoji", strings.Repeat("📚🗂️ notes ", 40)},     // 4-byte runes
		{"ascii control", strings.Repeat("plain ", 40)}, // passes without the fix
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every limit that lands inside the string, so one of them is
			// guaranteed to fall in the middle of a rune.
			for limit := 1; limit < len(tc.text); limit++ {
				got := compactText(tc.text, limit)
				if !utf8.ValidString(got) {
					t.Fatalf("limit %d produced invalid UTF-8: %q", limit, got)
				}
				body := strings.TrimSuffix(got, "…")
				if len(body) > limit {
					t.Fatalf("limit %d produced %d bytes: %q", limit, len(body), got)
				}
				if !strings.HasPrefix(tc.text, body) {
					t.Fatalf("limit %d altered the text: %q", limit, got)
				}
			}
		})
	}
}

func TestIncompleteDoneReasonOnlyMatchesLengthStops(t *testing.T) {
	if !isIncompleteDoneReason("length") || !isIncompleteDoneReason("context_window") {
		t.Fatal("length/context stop should continue")
	}
	if isIncompleteDoneReason("stop") || isIncompleteDoneReason("") {
		t.Fatal("ordinary stop should not continue")
	}
}
