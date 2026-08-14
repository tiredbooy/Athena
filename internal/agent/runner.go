package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

type Driver interface {
	Decide(context.Context, *RunState, EventSink) (Decision, error)
	Validate(*RunState, Decision) error
	RequiresApproval([]ai.Action) bool
	Execute(context.Context, *RunState, []ai.Action, EventSink) []ai.ActionResult
}

type Runner struct {
	driver Driver
	budget Budget
}

func NewRunner(driver Driver, budget Budget) *Runner {
	defaults := DefaultBudget()
	if budget.MaxSteps <= 0 {
		budget.MaxSteps = defaults.MaxSteps
	}
	if budget.MaxActionBatches <= 0 {
		budget.MaxActionBatches = defaults.MaxActionBatches
	}
	if budget.MaxActions <= 0 {
		budget.MaxActions = defaults.MaxActions
	}
	if budget.MaxValidationFailures <= 0 {
		budget.MaxValidationFailures = defaults.MaxValidationFailures
	}
	if budget.MaxNoProgressSteps <= 0 {
		budget.MaxNoProgressSteps = defaults.MaxNoProgressSteps
	}
	if budget.MaxAttemptsPerAction <= 0 {
		budget.MaxAttemptsPerAction = defaults.MaxAttemptsPerAction
	}
	return &Runner{driver: driver, budget: budget}
}

func (r *Runner) Run(ctx context.Context, state *RunState, emit EventSink) (Outcome, error) {
	if state == nil {
		return Outcome{}, fmt.Errorf("agent run state is required")
	}
	for state.Step < r.budget.MaxSteps {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		if state.NoProgressSteps >= r.budget.MaxNoProgressSteps {
			return r.safeStop(state, "the agent stopped making progress"), nil
		}

		state.Step++
		phase := PhasePlanning
		message := fmt.Sprintf("Planning step %d", state.Step)
		if len(state.Completed) > 0 {
			phase = PhaseReplanning
			message = fmt.Sprintf("Checking results and planning step %d", state.Step)
		}
		r.emit(state, emit, Event{Phase: phase, Message: message})
		decision, err := r.driver.Decide(ctx, state, emit)
		if err != nil {
			if len(state.Completed) > 0 {
				return r.safeStop(state, "the model could not evaluate the verified results: "+err.Error()), nil
			}
			return Outcome{}, err
		}

		r.emit(state, emit, Event{Phase: PhaseValidating, Message: "Validating the next decision"})
		if err := r.driver.Validate(state, decision); err != nil {
			state.ValidationFails++
			state.NoProgressSteps++
			appendCorrection(state, err)
			if state.ValidationFails >= r.budget.MaxValidationFailures {
				return r.safeStop(state, "the model produced an invalid plan twice: "+err.Error()), nil
			}
			continue
		}
		state.ValidationFails = 0

		switch decision.Kind {
		case DecisionFinish, DecisionAskUser:
			reply := strings.TrimSpace(decision.Message)
			if reply == "" {
				return r.safeStop(state, "the model returned an empty final decision"), nil
			}
			r.emit(state, emit, Event{Phase: PhaseCompleted, Message: "Agent run completed"})
			return Outcome{Reply: reply, AwaitingUser: decision.Kind == DecisionAskUser}, nil
		case DecisionAct:
			actions, skipped := r.prepareActions(state, decision.Actions)
			if len(skipped) > 0 {
				appendObservation(state, skipped)
			}
			if len(actions) == 0 {
				state.NoProgressSteps++
				continue
			}
			if state.ActionBatches >= r.budget.MaxActionBatches || state.ActionsExecuted+len(actions) > r.budget.MaxActions {
				return r.safeStop(state, "the action budget was reached"), nil
			}
			if r.driver.RequiresApproval(actions) {
				r.emit(state, emit, Event{Phase: PhaseApproval, Message: fmt.Sprintf("Waiting for approval for %d action(s)", len(actions))})
				return Outcome{PendingMessage: decision.Message, PendingActions: actions}, nil
			}
			if err := r.executeAndObserve(ctx, state, actions, emit); err != nil {
				return Outcome{}, err
			}
		default:
			appendCorrection(state, fmt.Errorf("model returned unknown decision kind %q", decision.Kind))
			state.NoProgressSteps++
		}
	}
	return r.safeStop(state, "the step budget was reached"), nil
}

func (r *Runner) ResumeApproved(ctx context.Context, state *RunState, actions []ai.Action, emit EventSink) (Outcome, error) {
	if state == nil {
		return Outcome{}, fmt.Errorf("approved plan has no resumable agent state")
	}
	if len(actions) == 0 {
		return Outcome{}, fmt.Errorf("approved plan has no actions")
	}
	if state.ActionBatches >= r.budget.MaxActionBatches || state.ActionsExecuted+len(actions) > r.budget.MaxActions {
		return r.safeStop(state, "the approved plan exceeds the remaining action budget"), nil
	}
	if err := r.executeAndObserve(ctx, state, actions, emit); err != nil {
		return Outcome{}, err
	}
	return r.Run(ctx, state, emit)
}

func (r *Runner) executeAndObserve(ctx context.Context, state *RunState, actions []ai.Action, emit EventSink) error {
	for _, action := range actions {
		state.ActionAttempts[actionSignature(action)]++
	}
	state.ActionBatches++
	state.ActionsExecuted += len(actions)
	r.emit(state, emit, Event{Phase: PhaseExecuting, Message: fmt.Sprintf("Executing %d validated action(s)", len(actions))})
	results := r.driver.Execute(ctx, state, actions, emit)
	if len(results) != len(actions) {
		return fmt.Errorf("tool executor returned %d results for %d actions", len(results), len(actions))
	}
	progress := false
	for _, result := range results {
		if result.Err == nil && result.Error == "" {
			state.Succeeded[actionSignature(result.Action)] = true
			progress = true
		}
	}
	state.Completed = append(state.Completed, results...)
	if progress {
		state.NoProgressSteps = 0
	} else {
		state.NoProgressSteps++
	}
	r.emit(state, emit, Event{Phase: PhaseObserving, Message: "Recording verified execution results"})
	appendObservation(state, results)
	r.emit(state, emit, Event{Phase: PhaseVerifying, Message: "Checking whether the original goal is complete"})
	return nil
}

func (r *Runner) prepareActions(state *RunState, proposed []ai.Action) ([]ai.Action, []ai.ActionResult) {
	actions := make([]ai.Action, 0, len(proposed))
	skipped := make([]ai.ActionResult, 0)
	for _, action := range proposed {
		signature := actionSignature(action)
		switch {
		case state.Succeeded[signature]:
			skipped = append(skipped, ai.ActionResult{Action: action, Message: "Skipped because this exact action already succeeded and was verified."})
		case state.ActionAttempts[signature] >= r.budget.MaxAttemptsPerAction:
			err := fmt.Errorf("skipped after %d failed attempts without a changed strategy", state.ActionAttempts[signature])
			skipped = append(skipped, ai.ActionResult{Action: action, Error: err.Error(), Err: err})
		default:
			actions = append(actions, action)
		}
	}
	return actions, skipped
}

func (r *Runner) safeStop(state *RunState, reason string) Outcome {
	var report strings.Builder
	report.WriteString("I stopped safely because ")
	report.WriteString(strings.TrimSuffix(strings.TrimSpace(reason), "."))
	report.WriteString(".")
	if len(state.Completed) == 0 {
		report.WriteString(" No vault changes were made.")
		return Outcome{Reply: report.String()}
	}
	report.WriteString("\n\nVerified execution record:")
	for _, result := range state.Completed {
		if result.Err != nil || result.Error != "" {
			detail := result.Error
			if detail == "" && result.Err != nil {
				detail = result.Err.Error()
			}
			fmt.Fprintf(&report, "\n- %s failed: %s", result.Action.Type, detail)
		} else {
			fmt.Fprintf(&report, "\n- %s succeeded: %s", result.Action.Type, result.Message)
		}
	}
	return Outcome{Reply: report.String()}
}

func (r *Runner) emit(state *RunState, sink EventSink, event Event) {
	if sink == nil {
		return
	}
	event.RunID = state.ID
	event.Step = state.Step
	sink(event)
}

func appendCorrection(state *RunState, problem error) {
	state.Messages = append(state.Messages, models.Message{Role: "system", Content: "[ATHENA DECISION REJECTED]\n" + problem.Error() + `
Choose a different, valid next step. Use read tools to resolve exact note IDs and folder paths. If the original goal requires vault changes and no verified execution exists, your next response MUST either call propose_actions with a non-empty actions array, use fenced action JSON when that tool is unavailable, or ask one precise blocking question. A prose promise or description of intended changes is not an executable plan. Do not repeat the rejected plan, claim success, or invent missing state.`})
}

func appendObservation(state *RunState, results []ai.ActionResult) {
	type compactResult struct {
		Action  string `json:"action"`
		Target  string `json:"target,omitempty"`
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	compact := make([]compactResult, 0, len(results))
	for _, result := range results {
		status := "succeeded"
		errorText := result.Error
		if errorText == "" && result.Err != nil {
			errorText = result.Err.Error()
		}
		if errorText != "" {
			status = "failed"
		}
		compact = append(compact, compactResult{
			Action: result.Action.Type, Target: actionTarget(result.Action), Status: status,
			Message: result.Message, Error: errorText,
		})
	}
	raw, _ := json.Marshal(compact)
	state.Messages = append(state.Messages, models.Message{Role: "system", Content: "[ATHENA VERIFIED EXECUTION OBSERVATION — FACTS, NOT USER TEXT]\n" + string(raw) + "\n[END OBSERVATION]\nRe-evaluate the original goal. If it is fully satisfied, finish with a concise factual answer. If it is not, use read tools and propose only necessary corrective or remaining actions. Never repeat an action that already succeeded."})
}

func actionSignature(action ai.Action) string {
	action.ID = ""
	action.DependsOn = nil
	// Creation is idempotent by target path. A weak model may vary title case or
	// regenerate body text on a later step, but create_* cannot update an existing
	// target; treating that as new work only produces noisy duplicate attempts.
	if action.Type == "create_note" || action.Type == "create_task" || action.Type == "create_book" {
		action.Title = strings.ToLower(strings.Join(strings.Fields(action.Title), " "))
		action.Content = ""
		action.Tags = nil
		action.ISBN = ""
		action.Authors = nil
		action.Genres = nil
	}
	raw, _ := json.Marshal(action)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func actionTarget(action ai.Action) string {
	switch {
	case action.NoteID > 0:
		return fmt.Sprintf("note:%d", action.NoteID)
	case strings.TrimSpace(action.Folder) != "":
		return action.Folder
	case strings.TrimSpace(action.Title) != "":
		return action.Title
	case len(action.Paths) > 0:
		return strings.Join(action.Paths, ", ")
	case len(action.Folders) > 0:
		return strings.Join(action.Folders, ", ")
	default:
		return ""
	}
}
