package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

// R-01: Athena's baseline is a ~2B local model. It does not fail by returning
// an error — it returns confident prose, an action name that does not exist, or
// a promise to act with nothing attached. Every test here drives the runner with
// one of those shapes and asserts the user-visible outcome: a safe stop that
// never claims the vault changed. Assertions are on Outcome and the ledger, not
// on wording, so rephrasing a message does not break them.

// "I'll create it for you." with an empty actions array. The runner must not
// loop on the empty promise, and must not hand the promise back as the answer.
func TestRunnerStopsWhenWeakModelPromisesActionWithNoActions(t *testing.T) {
	promise := "Sure! I'll create that note for you now."
	// More decisions than MaxSteps: if the runner ever looped, it would keep
	// consuming them instead of stopping on its own.
	decisions := make([]Decision, 12)
	for i := range decisions {
		decisions[i] = Decision{Kind: DecisionAct, Message: promise}
	}
	driver := &scriptedDriver{decisions: decisions}
	state := NewRunState("weak-empty-actions", "create a note", []models.Message{{Role: "user", Content: "create a note"}})

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if driver.decisionIndex >= len(decisions) {
		t.Fatalf("runner consumed every scripted decision (%d); it did not stop on its own", driver.decisionIndex)
	}
	if outcome.Reply == promise {
		t.Fatal("the model's unbacked promise was returned as the answer")
	}
	if outcome.Reply == "" {
		t.Fatal("the run ended with no explanation at all")
	}
	if driver.executeCalls != 0 || len(outcome.Ledger) != 0 || len(state.Completed) != 0 {
		t.Fatalf("nothing ran, yet execute calls=%d ledger=%d", driver.executeCalls, len(outcome.Ledger))
	}
}

// The "wrong action name" shape: every plan the model produces is rejected by
// validation. The runner spends its correction budget, then stops safely.
func TestRunnerStopsAfterBudgetedCorrectionWhenEveryPlanIsInvalid(t *testing.T) {
	claim := "Done — I renamed the folder."
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Message: claim, Actions: []ai.Action{{Type: "rename_folder", Folder: "work"}}},
			{Kind: DecisionAct, Message: claim, Actions: []ai.Action{{Type: "rename_folder", Folder: "work"}}},
			{Kind: DecisionAct, Message: claim, Actions: []ai.Action{{Type: "rename_folder", Folder: "work"}}},
		},
		validate: func(int, *RunState, Decision) error {
			return errors.New("unknown action type \"rename_folder\"")
		},
	}
	state := NewRunState("weak-invalid-plan", "rename my work folder", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if driver.validationCalls != DefaultBudget().MaxValidationFailures {
		t.Fatalf("validation calls = %d, want the budgeted %d", driver.validationCalls, DefaultBudget().MaxValidationFailures)
	}
	if outcome.Reply == claim {
		t.Fatal("the model's success claim survived a run where nothing was executed")
	}
	if driver.executeCalls != 0 || len(outcome.Ledger) != 0 {
		t.Fatalf("an invalid plan reached execution: calls=%d ledger=%d", driver.executeCalls, len(outcome.Ledger))
	}
	if !strings.Contains(messageText(state.Messages), "[ATHENA DECISION REJECTED]") {
		t.Fatal("the model was never told why its plan was rejected")
	}
}

// A 2B model can run out of tokens mid-sentence and finish with nothing. A blank
// reply must become a stated stop, not a blank success.
func TestRunnerRejectsEmptyFinalMessage(t *testing.T) {
	driver := &scriptedDriver{decisions: []Decision{{Kind: DecisionFinish, Message: "   "}}}
	state := NewRunState("weak-empty-finish", "summarise my notes", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(outcome.Reply) == "" {
		t.Fatal("an empty model answer was passed through as the run's answer")
	}
	if outcome.AwaitingUser || len(outcome.Ledger) != 0 || driver.executeCalls != 0 {
		t.Fatalf("outcome = %+v, want a plain safe stop", outcome)
	}
}

// A weak model must not be able to hide finished work behind a stop: when a batch
// succeeded before the run went wrong, the safe stop still carries the verified
// record so the user learns the vault changed.
func TestRunnerSafeStopStillReportsVerifiedWork(t *testing.T) {
	action := ai.Action{Type: "create_note", Title: "Plan", Folder: "work"}
	driver := &scriptedDriver{
		decisions: []Decision{
			{Kind: DecisionAct, Actions: []ai.Action{action}},
			{Kind: DecisionFinish, Message: ""},
		},
		execute: func(int, []ai.Action) []ai.ActionResult {
			return []ai.ActionResult{{Action: action, Message: "note created and verified"}}
		},
	}
	state := NewRunState("weak-hidden-work", "create a plan note", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	records := outcome.Records()
	if len(records) != 1 {
		t.Fatalf("ledger records = %+v, want the one executed action", records)
	}
	if records[0].Action != "create_note" || records[0].Status != "succeeded" || records[0].Target != "work" {
		t.Fatalf("record = %+v, want a succeeded create_note targeting work", records[0])
	}
}

// A model that answers with a tool-ish shape the runner does not know must be
// corrected and then stopped, never treated as a completed answer.
func TestRunnerStopsOnUnknownDecisionKind(t *testing.T) {
	decisions := make([]Decision, 8)
	for i := range decisions {
		decisions[i] = Decision{Kind: DecisionKind("tool_call"), Message: "I created it."}
	}
	driver := &scriptedDriver{decisions: decisions}
	state := NewRunState("weak-unknown-kind", "create a note", nil)

	outcome, err := NewRunner(driver, DefaultBudget()).Run(t.Context(), state, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if driver.decisionIndex >= len(decisions) {
		t.Fatalf("runner consumed every scripted decision (%d); it did not stop on its own", driver.decisionIndex)
	}
	if outcome.Reply == "I created it." || len(outcome.Ledger) != 0 || driver.executeCalls != 0 {
		t.Fatalf("outcome = %+v, want a safe stop with no claimed work", outcome)
	}
	if !strings.Contains(messageText(state.Messages), "[ATHENA DECISION REJECTED]") {
		t.Fatal("the model was never told its decision kind was unusable")
	}
}
