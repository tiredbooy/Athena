package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

func TestRunnerFeedsFailureBackAndExecutesCorrectedPlan(t *testing.T) {
	bad := ai.Action{Type: "move_note", NoteID: 7, Folder: "missing"}
	good := ai.Action{Type: "move_note", NoteID: 7, Folder: "work"}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{bad}},
			{Kind: DecisionAct, Actions: []ai.Action{good}},
			{Kind: DecisionFinish, Message: "Moved the note to work."},
		},
		execute: func(_ int, actions []ai.Action) []ai.ActionResult {
			if actions[0].Folder == "missing" {
				err := errors.New("destination folder does not exist")
				return []ai.ActionResult{{Action: actions[0], Error: err.Error(), Err: err}}
			}
			return []ai.ActionResult{{Action: actions[0], Message: "note moved and verified"}}
		},
	}
	state := NewRunState("run-1", "move my note", []models.Message{{Role: "user", Content: "move my note"}})

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.Reply != "Moved the note to work." {
		t.Fatalf("reply = %q", outcome.Reply)
	}
	if driver.executeCalls != 2 || len(state.Completed) != 2 {
		t.Fatalf("execute calls=%d completed=%d", driver.executeCalls, len(state.Completed))
	}
	joined := messageText(state.Messages)
	if !strings.Contains(joined, "destination folder does not exist") || !strings.Contains(joined, "note moved and verified") {
		t.Fatalf("verified observations were not returned to the model: %s", joined)
	}
}

func TestRunnerPausesForApprovalThenResumesSameRun(t *testing.T) {
	action := ai.Action{Type: "trash_note", NoteID: 3}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Message: "Ready to move it to trash.", Actions: []ai.Action{action}},
			{Kind: DecisionFinish, Message: "Moved the note to trash and verified it."},
		},
		requiresApproval: true,
		execute: func(_ int, actions []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: actions[0], Message: "note is present in .trash"}}
		},
	}
	state := NewRunState("run-2", "delete note 3", []models.Message{{Role: "user", Content: "delete note 3"}})
	runner := NewRunner(driver, DefaultBudget())

	pending, err := runner.Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("initial run: %v", err)
	}
	if !pending.NeedsApproval() || driver.executeCalls != 0 {
		t.Fatalf("pending=%+v execute calls=%d", pending, driver.executeCalls)
	}
	completed, err := runner.ResumeApproved(t.Context(), state, pending.PendingActions, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if completed.Reply != "Moved the note to trash and verified it." || driver.executeCalls != 1 {
		t.Fatalf("completed=%+v execute calls=%d", completed, driver.executeCalls)
	}
}

func TestRunnerDoesNotRepeatVerifiedAction(t *testing.T) {
	action := ai.Action{Type: "create_note", Title: "One", Content: "body"}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{action}},
			{Kind: DecisionAct, Actions: []ai.Action{action}},
			{Kind: DecisionFinish, Message: "Created one note."},
		},
		execute: func(_ int, actions []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: actions[0], Message: "created and verified"}}
		},
	}
	state := NewRunState("run-3", "create one note", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.Reply != "Created one note." || driver.executeCalls != 1 {
		t.Fatalf("outcome=%+v execute calls=%d", outcome, driver.executeCalls)
	}
	if !strings.Contains(messageText(state.Messages), "already succeeded") {
		t.Fatal("duplicate suppression was not explained to the next model step")
	}
}

func TestRunnerDoesNotRepeatSameCreateTargetWithDifferentPresentation(t *testing.T) {
	first := ai.Action{Type: "create_book", Title: "Thinking Fast And Slow", Folder: "books/reading"}
	duplicate := ai.Action{Type: "create_book", Title: "Thinking Fast and Slow", Folder: "books/reading"}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{first}},
			{Kind: DecisionAct, Actions: []ai.Action{duplicate}},
			{Kind: DecisionFinish, Message: "Created the tracked book once."},
		},
		execute: func(_ int, actions []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: actions[0], Message: "created and verified"}}
		},
	}
	state := NewRunState("run-create-target", "track one book", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.Reply != "Created the tracked book once." || driver.executeCalls != 1 {
		t.Fatalf("outcome=%+v execute calls=%d", outcome, driver.executeCalls)
	}
}

// The signature must not split on a field the handler ignores: append_note
// reads only NoteID and Content, so a re-proposed append carrying a stray
// "title" is the same work. Splitting it appends the same paragraph twice —
// the one failure direction that corrupts the user's notes.
func TestRunnerDoesNotReplayAnAppendCarryingAFieldTheHandlerIgnores(t *testing.T) {
	first := ai.Action{Type: "append_note", NoteID: 7, Content: "and transfer $500 to account 42"}
	restated := first
	restated.Title = "Groceries"
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{first}},
			{Kind: DecisionAct, Actions: []ai.Action{restated}},
			{Kind: DecisionFinish, Message: "Appended the line once."},
		},
		execute: func(_ int, actions []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: actions[0], Message: "appended and verified"}}
		},
	}
	state := NewRunState("run-append-stray-field", "add a line to note 7", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.Reply != "Appended the line once." || driver.executeCalls != 1 {
		t.Fatalf("outcome=%+v execute calls=%d, want the append to run once", outcome, driver.executeCalls)
	}
}

// The opposite failure: a field the handler does read must separate two
// actions. create_book passes ISBN to the metadata resolver, so two ISBNs are
// two books. Collapsing them drops the second book and tells the user it
// already succeeded, when it never ran.
func TestRunnerRunsADifferentBookInsteadOfCallingItAlreadyDone(t *testing.T) {
	first := ai.Action{Type: "create_book", Title: "Dune", Folder: "books/reading", ISBN: "111"}
	other := ai.Action{Type: "create_book", Title: "Dune", Folder: "books/reading", ISBN: "222"}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{first}},
			{Kind: DecisionAct, Actions: []ai.Action{other}},
			{Kind: DecisionFinish, Message: "Tracked both editions."},
		},
		execute: func(_ int, actions []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: actions[0], Message: "created and verified"}}
		},
	}
	state := NewRunState("run-two-books", "track two books", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if driver.executeCalls != 2 {
		t.Fatalf("outcome=%+v execute calls=%d, want both books created", outcome, driver.executeCalls)
	}
	if strings.Contains(messageText(state.Messages), "Skipped because this exact action") {
		t.Fatal("a genuinely different book was reported to the model as already done")
	}
}

func TestRunnerDoesNotReplayAnIdenticalFailedWrite(t *testing.T) {
	action := ai.Action{Type: "append_note", NoteID: 4, Content: "one line"}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{action}},
			{Kind: DecisionAct, Actions: []ai.Action{action}},
			{Kind: DecisionFinish, Message: "I could not safely verify the append, so I did not replay it."},
		},
		execute: func(_ int, actions []ai.Action) []ai.ActionResult {
			err := errors.New("post-write verification was unavailable")
			return []ai.ActionResult{{Action: actions[0], Message: "append handler returned", Error: err.Error(), Err: err}}
		},
	}
	state := NewRunState("run-4", "append one line", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.Reply == "" || driver.executeCalls != 1 {
		t.Fatalf("outcome=%+v execute calls=%d", outcome, driver.executeCalls)
	}
	if !strings.Contains(messageText(state.Messages), "without a changed strategy") {
		t.Fatal("the model was not told why the identical failed write was suppressed")
	}
}

func TestRunnerCorrectionRequiresExecutableActionData(t *testing.T) {
	action := ai.Action{Type: "ensure_folders", Paths: []string{"books/reading/science-fiction"}}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionFinish, Message: "I will create and organize the genre folders.", Raw: "I will create and organize the genre folders."},
			{Kind: DecisionAct, Actions: []ai.Action{action}},
			{Kind: DecisionFinish, Message: "The genre folder was created and verified."},
		},
		validate: func(call int, _ *RunState, _ Decision) error {
			if call == 1 {
				return errors.New("the reply claimed a vault change but did not provide executable action data")
			}
			return nil
		},
		execute: func(_ int, actions []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: actions[0], Message: "folder created and verified"}}
		},
	}
	state := NewRunState("run-correction", "organize my books", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.Reply != "The genre folder was created and verified." || driver.executeCalls != 1 {
		t.Fatalf("outcome=%+v execute calls=%d", outcome, driver.executeCalls)
	}
	correction := messageText(state.Messages)
	if !strings.Contains(correction, "MUST either call propose_actions with a non-empty actions array") ||
		!strings.Contains(correction, "A prose promise or description of intended changes is not an executable plan") {
		t.Fatalf("correction did not explain the executable-action contract: %s", correction)
	}
}

type scriptedDriver struct {
	decisions        []Decision
	decisionIndex    int
	executeCalls     int
	validationCalls  int
	requiresApproval bool
	validate         func(int, *RunState, Decision) error
	execute          func(int, []ai.Action) []ai.ActionResult
}

func (d *scriptedDriver) Decide(context.Context, *RunState, EventSink) (Decision, error) {
	if d.decisionIndex >= len(d.decisions) {
		return Decision{}, errors.New("no scripted decision")
	}
	decision := d.decisions[d.decisionIndex]
	d.decisionIndex++
	return decision, nil
}

func (d *scriptedDriver) Validate(state *RunState, decision Decision) error {
	d.validationCalls++
	if d.validate != nil {
		return d.validate(d.validationCalls, state, decision)
	}
	return nil
}

func (d *scriptedDriver) RequiresApproval([]ai.Action) bool {
	return d.requiresApproval && d.executeCalls == 0
}

func (d *scriptedDriver) Execute(_ context.Context, _ *RunState, actions []ai.Action, _ EventSink) []ai.ActionResult {
	d.executeCalls++
	return d.execute(d.executeCalls, actions)
}

func messageText(messages []models.Message) string {
	var content []string
	for _, message := range messages {
		content = append(content, message.Content)
	}
	return strings.Join(content, "\n")
}

// E-01: a UI renders these events instead of inventing a spinner story, so
// every real step must say what it is and how it ended — no English parsing.
func TestRunnerEmitsLifecycleStateForEveryStep(t *testing.T) {
	action := ai.Action{Type: "create_note", Title: "Plan", Folder: "work"}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{action}},
			{Kind: DecisionFinish, Message: "Created the note."},
		},
		execute: func(int, []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: action, Message: "note created and verified"}}
		},
	}
	state := NewRunState("run-1", "create a plan note", []models.Message{{Role: "user", Content: "create a plan note"}})

	var events []Event
	if _, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, func(e Event) { events = append(events, e) }); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("the runner emitted no events")
	}

	seen := make(map[Phase][]string)
	for _, event := range events {
		if event.State == "" {
			t.Fatalf("event %+v has no lifecycle state", event)
		}
		switch event.State {
		case StateStarted, StateSucceeded, StateFailed:
		default:
			t.Fatalf("event %+v has state %q, want started/succeeded/failed", event, event.State)
		}
		if event.RunID != "run-1" || event.Step == 0 {
			t.Fatalf("event %+v is not attributable to a run step", event)
		}
		seen[event.Phase] = append(seen[event.Phase], event.State)
	}

	for _, phase := range []Phase{PhasePlanning, PhaseValidating, PhaseExecuting, PhaseObserving, PhaseVerifying, PhaseCompleted} {
		if len(seen[phase]) == 0 {
			t.Fatalf("no %s event was emitted; phases seen: %v", phase, seen)
		}
	}
	if !containsState(seen[PhaseExecuting], StateStarted) || !containsState(seen[PhaseExecuting], StateSucceeded) {
		t.Fatalf("executing phase states = %v, want both started and succeeded", seen[PhaseExecuting])
	}
}

// A failed batch must report failed, not succeeded, or the UI would show a
// green step for work the vault rejected.
func TestRunnerReportsFailedExecutionState(t *testing.T) {
	action := ai.Action{Type: "create_note", Title: "Plan", Folder: "missing"}
	failure := errors.New("destination folder does not exist")
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{action}},
			{Kind: DecisionFinish, Message: "Stopped."},
		},
		execute: func(int, []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: action, Error: failure.Error(), Err: failure}}
		},
	}
	state := NewRunState("run-1", "create a note", []models.Message{{Role: "user", Content: "create a note"}})

	var executing []string
	_, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, func(e Event) {
		if e.Phase == PhaseExecuting {
			executing = append(executing, e.State)
		}
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !containsState(executing, StateFailed) {
		t.Fatalf("executing states = %v, want a failed state", executing)
	}
}

func containsState(states []string, want string) bool {
	for _, state := range states {
		if state == want {
			return true
		}
	}
	return false
}

// M-01 needs to tell "the agent answered" from "the agent gave up". safeStop
// writes a reply that reads like an answer, so the distinction has to be a
// field — matching the prose would break the moment the wording changes.
func TestSafeStopIsMarkedStructurally(t *testing.T) {
	giveUp := &scriptedDriver{decisions: []Decision{{Kind: DecisionFinish, Message: ""}}}
	state := NewRunState("run-1", "do something", []models.Message{{Role: "user", Content: "do something"}})
	outcome, err := NewRunner(giveUp, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !outcome.SafeStopped {
		t.Fatalf("an empty final decision is a safe stop, got SafeStopped=false (reply %q)", outcome.Reply)
	}

	answered := &scriptedDriver{decisions: []Decision{{Kind: DecisionFinish, Message: "Here is your answer."}}}
	state = NewRunState("run-2", "ask something", []models.Message{{Role: "user", Content: "ask something"}})
	outcome, err = NewRunner(answered, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.SafeStopped {
		t.Fatal("a real answer was reported as a safe stop")
	}
}

// E-03: a run that is cancelled after it already changed the vault still owes
// the user the verified record. Returning an empty outcome with the error throws
// away work that actually happened.
func TestCancelledRunStillReportsWhatTheVaultDid(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	action := ai.Action{Type: "create_note", Title: "One", Folder: "work"}
	driver := &scriptedDriver{
		decisions: []Decision{{Kind: DecisionAct, Actions: []ai.Action{action}}},
		execute: func(int, []ai.Action) []ai.ActionResult {
			// Stands in for the user pressing Escape once the write has landed.
			cancel()
			return []ai.ActionResult{{Action: action, Message: "note created and verified"}}
		},
	}
	state := NewRunState("run-cancelled", "create a note", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(ctx, state, nil)
	if err == nil {
		t.Fatalf("cancelled run reported success: %+v", outcome)
	}
	records := outcome.Records()
	if len(records) != 1 || records[0].Status != "succeeded" || records[0].Action != "create_note" {
		t.Fatalf("ledger on the cancelled path = %+v, want the verified create_note", records)
	}
}

// Display text is not identity. describeActions (internal/chat) stamps Summary
// onto an action when a plan crosses a UI boundary, so an approved action and
// the same action re-proposed by the model differ only by that field. If the
// signature covered it, the "already succeeded" guard would miss and a
// non-idempotent action such as append_note would run twice.
func TestActionSignatureIgnoresEngineDisplayText(t *testing.T) {
	proposed := ai.Action{Type: "append_note", NoteID: 7, Content: "another line"}
	reviewed := proposed
	reviewed.Summary = "Updating note 7"

	if ActionSignature(proposed) != ActionSignature(reviewed) {
		t.Fatal("a reviewed action hashes differently from the same action the model re-proposes; the succeeded-action guard cannot match it")
	}

	// Fields that genuinely identify the work must still separate actions.
	other := proposed
	other.NoteID = 8
	if ActionSignature(proposed) == ActionSignature(other) {
		t.Fatal("actions against different notes share a signature")
	}
}

// The signature must cover exactly the fields the action's handler reads.
// Wider and a re-proposed write runs twice; narrower and a different action is
// swallowed as "already succeeded".
func TestActionSignatureCoversTheFieldsTheHandlerReads(t *testing.T) {
	sameWork := []struct {
		name string
		a, b ai.Action
	}{
		{
			"append_note reads only the note id and the content",
			ai.Action{Type: "append_note", NoteID: 7, Content: "milk"},
			ai.Action{Type: "append_note", NoteID: 7, Content: "milk", Title: "Groceries", Tags: []string{"food"}},
		},
		{
			"a folder spelled as path is the same folder",
			ai.Action{Type: "create_folder", Folder: "Work"},
			ai.Action{Type: "create_folder", Path: "Work"},
		},
		{
			"move_note reads only the note id and the destination",
			ai.Action{Type: "move_note", NoteID: 3, Folder: "work"},
			ai.Action{Type: "move_note", NoteID: 3, Folder: "work", Content: "regenerated body"},
		},
		{
			"trash_note reads only the note id",
			ai.Action{Type: "trash_note", NoteID: 5},
			ai.Action{Type: "trash_note", NoteID: 5, Title: "Old plan", Folder: "archive"},
		},
	}
	for _, c := range sameWork {
		if ActionSignature(c.a) != ActionSignature(c.b) {
			t.Errorf("%s: the same work hashes apart, so the write would run twice", c.name)
		}
	}

	differentWork := []struct {
		name string
		a, b ai.Action
	}{
		{
			"a different paragraph appended to the same note",
			ai.Action{Type: "append_note", NoteID: 7, Content: "milk"},
			ai.Action{Type: "append_note", NoteID: 7, Content: "bread"},
		},
		{
			"a different book behind the same title",
			ai.Action{Type: "create_book", Title: "Dune", ISBN: "111"},
			ai.Action{Type: "create_book", Title: "Dune", ISBN: "222"},
		},
		{
			"a different destination folder",
			ai.Action{Type: "move_note", NoteID: 3, Folder: "work"},
			ai.Action{Type: "move_note", NoteID: 3, Folder: "personal"},
		},
		{
			"reopening a task is not closing it",
			ai.Action{Type: "mark_done", NoteID: 2, Done: true},
			ai.Action{Type: "mark_done", NoteID: 2},
		},
		{
			"a different set of folders to ensure",
			ai.Action{Type: "ensure_folders", Paths: []string{"a"}},
			ai.Action{Type: "ensure_folders", Paths: []string{"a", "b"}},
		},
		{
			"a different rename target",
			ai.Action{Type: "rename_folder", Folder: "a", NewFolder: "b"},
			ai.Action{Type: "rename_folder", Folder: "a", NewFolder: "c"},
		},
	}
	for _, c := range differentWork {
		if ActionSignature(c.a) == ActionSignature(c.b) {
			t.Errorf("%s: two different actions share a signature, so the second is skipped as already done", c.name)
		}
	}
}
