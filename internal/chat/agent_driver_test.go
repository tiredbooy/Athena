package chat

import (
	"context"
	"sync"
	"testing"

	"github.com/tiredbooy/internal/agent"
	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/tools"
)

// The execution ledger (internal/agent) and the executing progress events
// emitted here describe the same action to the same user. Both sides must name
// its target identically, so this pins the chat side to agent.ActionTarget
// rather than to any label rewritten locally.
func TestExecuteEventTargetMatchesLedgerTarget(t *testing.T) {
	actions := []ai.Action{
		{Type: "probe", NoteID: 7, Folder: "Books", Title: "ignored"},
		{Type: "probe", Folder: "Books/Reading"},
		{Type: "probe", Folder: "   ", Title: "Dune"},
		{Type: "probe", Paths: []string{"a.md", "b.md"}},
		{Type: "probe", Folders: []string{"Books", "Notes"}},
		{Type: "probe"},
	}

	dispatcher := tools.NewDispatcher()
	dispatcher.Register("probe", func(context.Context, ai.Action) (string, error) { return "ok", nil })
	session := NewSession(NewLoop(nil, nil, nil, nil, dispatcher, nil))
	driver := sessionAgentDriver{session: session}

	// RunBatch may execute actions concurrently, so collect targets into a set
	// under a lock instead of assuming events arrive in the listed order.
	var mu sync.Mutex
	seen := map[string]bool{}
	emit := func(event agent.Event) {
		if event.Phase != agent.PhaseExecuting {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		seen[event.Target] = true
	}
	driver.Execute(context.Background(), &agent.RunState{ID: "run", Step: 1}, actions, emit)

	for _, action := range actions {
		want := agent.ActionTarget(action)
		if !seen[want] {
			t.Fatalf("no executing event carried the ledger target %q for %+v; got %v", want, action, seen)
		}
	}
}
