// Package agent owns Athena's bounded decide-act-observe lifecycle. It is an
// application-layer coordinator: models propose work, tools perform work, and
// the runner decides when to continue, pause for approval, or stop safely.
package agent

import (
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

type Phase string

const (
	PhasePlanning     Phase = "planning"
	PhaseReading      Phase = "reading"
	PhaseSearching    Phase = "searching"
	PhaseProviderWait Phase = "provider_wait"
	PhaseValidating   Phase = "validating"
	PhaseApproval     Phase = "approval"
	PhaseExecuting    Phase = "executing"
	PhaseObserving    Phase = "observing"
	PhaseVerifying    Phase = "verifying"
	PhaseReplanning   Phase = "replanning"
	PhaseCompleted    Phase = "completed"
)

type Budget struct {
	MaxSteps              int
	MaxActionBatches      int
	MaxActions            int
	MaxValidationFailures int
	MaxNoProgressSteps    int
	MaxAttemptsPerAction  int
}

func DefaultBudget() Budget {
	return Budget{
		MaxSteps: 6, MaxActionBatches: 4, MaxActions: 24,
		MaxValidationFailures: 2, MaxNoProgressSteps: 2, MaxAttemptsPerAction: 1,
	}
}

type RunState struct {
	ID              string
	Goal            string
	SuccessCriteria []string
	Messages        []models.Message
	StartedAt       time.Time
	Step            int
	ActionBatches   int
	ActionsExecuted int
	ValidationFails int
	NoProgressSteps int
	ContextSupplied bool
	ExpectedAction  bool
	Completed       []ai.ActionResult
	ActionAttempts  map[string]int
	Succeeded       map[string]bool
}

func NewRunState(id, goal string, messages []models.Message) *RunState {
	return &RunState{
		ID: id, Goal: goal, Messages: append([]models.Message(nil), messages...), StartedAt: time.Now(),
		SuccessCriteria: []string{
			"Every requested read is answered from available evidence.",
			"Every requested mutation is validated, executed, and verified.",
			"Any unresolved failure or ambiguity is stated instead of guessed.",
		},
		ActionAttempts: make(map[string]int), Succeeded: make(map[string]bool),
	}
}

type DecisionKind string

const (
	DecisionFinish  DecisionKind = "finish"
	DecisionAskUser DecisionKind = "ask_user"
	DecisionAct     DecisionKind = "act"
)

type Decision struct {
	Kind    DecisionKind
	Message string
	Raw     string
	Actions []ai.Action
}

type Event struct {
	RunID    string
	Step     int
	Phase    Phase
	Message  string
	Provider string
	Model    string
	Tool     string
	Target   string
	State    string
}

type EventSink func(Event)

type Outcome struct {
	Reply          string
	PendingMessage string
	PendingActions []ai.Action
	// AwaitingUser distinguishes a clarification from a completed answer. The
	// chat session uses it to preserve the active goal across user turns.
	AwaitingUser bool
}

func (o Outcome) NeedsApproval() bool { return len(o.PendingActions) > 0 }
