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

// A step's State is its lifecycle, not a judgement of the answer: "started"
// when Athena begins real work, and "succeeded" or "failed" once that work has
// an outcome. A UI can therefore show progress without parsing English.
const (
	StateStarted   = "started"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
)

// Event is one factual step. Phase says what kind of work it is; Tool and
// Target say which operation and on what, for the steps that have them.
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
	// SafeStopped marks a run that gave up rather than answering: the budget ran
	// out, validation failed twice, the model returned nothing usable. The reply
	// reads like an answer, so callers that must tell "finished" from "gave up"
	// need this flag — matching the reply text would be exactly the English
	// parsing this architecture exists to avoid.
	SafeStopped bool
	// Ledger is every action this run actually executed, with the verified
	// result. It travels with the outcome on every path — including a normal
	// finish — because the model's closing sentence is not evidence. A terse
	// model, or a 2B model that says nothing at all, must not be able to hide
	// what happened to the vault.
	Ledger []ai.ActionResult
}

// LedgerRecord is one verified action outcome in transport-safe form.
type LedgerRecord struct {
	Action  string `json:"action"`
	Target  string `json:"target,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Records flattens the ledger for a UI or protocol boundary.
func (o Outcome) Records() []LedgerRecord {
	records := make([]LedgerRecord, 0, len(o.Ledger))
	for _, result := range o.Ledger {
		status, errorText := "succeeded", result.Error
		if errorText == "" && result.Err != nil {
			errorText = result.Err.Error()
		}
		if errorText != "" {
			status = "failed"
		}
		records = append(records, LedgerRecord{
			Action: result.Action.Type, Target: ActionTarget(result.Action),
			Status: status, Message: result.Message, Error: errorText,
		})
	}
	return records
}

func (o Outcome) NeedsApproval() bool { return len(o.PendingActions) > 0 }
