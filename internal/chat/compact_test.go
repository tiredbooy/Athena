package chat

import (
	"strings"
	"testing"

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

func TestIncompleteDoneReasonOnlyMatchesLengthStops(t *testing.T) {
	if !isIncompleteDoneReason("length") || !isIncompleteDoneReason("context_window") {
		t.Fatal("length/context stop should continue")
	}
	if isIncompleteDoneReason("stop") || isIncompleteDoneReason("") {
		t.Fatal("ordinary stop should not continue")
	}
}
