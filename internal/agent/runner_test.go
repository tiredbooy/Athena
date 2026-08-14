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
	duplicate := ai.Action{Type: "create_book", Title: "Thinking Fast and Slow", Folder: "books/reading", ISBN: "9780141033570"}
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
