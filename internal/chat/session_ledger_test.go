package chat

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/agent"
	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/tools"
)

// E-03: a verified execution record belongs to the turn that produced it.
// Publishing an earlier turn's record is worse than publishing nothing, because
// it describes vault changes this turn did not make.
func TestLedgerDoesNotSurviveIntoALaterTurn(t *testing.T) {
	session := NewSession(NewLoop(nil, nil, nil, nil, nil, nil))
	session.setLastLedger([]agent.LedgerRecord{{Action: "create_note", Target: "work/one", Status: "succeeded"}})

	reply, err := session.ApprovePlan(t.Context(), "")
	if err != nil {
		t.Fatalf("approve with nothing pending: %v", err)
	}
	if reply != "There is no pending change to confirm." {
		t.Fatalf("reply = %q", reply)
	}
	if ledger := session.LastLedger(); len(ledger) != 0 {
		t.Fatalf("a turn that changed nothing republished an earlier turn's ledger: %+v", ledger)
	}
}

// ledgerTurnProvider proposes one directly-executable vault write. A cancelled
// turn never asks it twice: the runner exits at its cancellation check as soon
// as that write lands. failNow instead makes the model call fail before any
// action exists, which is the turn that has nothing to report.
type ledgerTurnProvider struct{ failNow bool }

func (p *ledgerTurnProvider) Name() string        { return "ChatGPT subscription" }
func (p *ledgerTurnProvider) ChatModel() string   { return "test" }
func (p *ledgerTurnProvider) SetChatModel(string) {}
func (p *ledgerTurnProvider) ChatModels(context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (p *ledgerTurnProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}
func (p *ledgerTurnProvider) ChatWithToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ai.ToolChatResult, error) {
	if p.failNow {
		return ai.ToolChatResult{}, errors.New("the model connection dropped")
	}
	return ai.ToolChatResult{Message: models.Message{Role: "assistant", Content: "Marking it done.\n\n```action\n{\"actions\":[{\"type\":\"mark_done\",\"note_id\":1}]}\n```"}}, nil
}

// E-03: the ledger rides every terminal event, not just the happy one. A turn
// that wrote to the vault and was then interrupted must still tell the client
// what it did — otherwise the UI shows an interruption over silent writes.
func TestTerminalEventsCarryThisTurnsLedger(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	newSession := func(provider ai.ChatProvider, dispatcher *tools.Dispatcher) *Session {
		retrievalService := retrieval.NewService(t.TempDir(), storage.NewNoteStore(db), storage.NewChunkStore(db), taskStateEmbedder{})
		return NewSession(NewLoop(provider, nil, nil, retrievalService, dispatcher, nil))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatcher := tools.NewDispatcher()
	dispatcher.Register("mark_done", func(context.Context, ai.Action) (string, error) { return "marked note 1 done", nil })

	var events []SessionEvent
	// Cancelling once the write is verified is the user pressing Escape while
	// Athena re-plans after its first real change.
	sink := func(event SessionEvent) {
		events = append(events, event)
		if event.Activity != nil && event.Activity.Tool == "mark_done" && event.Activity.State == "completed" {
			cancel()
		}
	}

	if _, err := newSession(&ledgerTurnProvider{}, dispatcher).SubmitWithEvents(ctx, "mark note 1 done", sink); err == nil {
		t.Fatal("cancelled turn reported success")
	}
	terminal := events[len(events)-1]
	if terminal.Type != EventCancelled {
		t.Fatalf("terminal event = %q, want %q", terminal.Type, EventCancelled)
	}
	if len(terminal.Ledger) != 1 || terminal.Ledger[0].Action != "mark_done" || terminal.Ledger[0].Status != "succeeded" {
		t.Fatalf("cancelled event ledger = %+v, want the verified mark_done record", terminal.Ledger)
	}

	// A turn that never reached execution owes the user silence, not an empty
	// artefact that reads as "the vault reports nothing happened".
	events = nil
	if _, err := newSession(&ledgerTurnProvider{failNow: true}, dispatcher).SubmitWithEvents(context.Background(), "mark note 1 done", sink); err == nil {
		t.Fatal("failed turn reported success")
	}
	terminal = events[len(events)-1]
	if terminal.Type != EventError || terminal.Ledger != nil {
		t.Fatalf("failed event = %+v, want an error event with no ledger", terminal)
	}
}
