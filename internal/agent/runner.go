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
		// E-03: every exit carries the verified ledger, error paths included. A
		// cancelled or failed run can already have changed the vault, and the user
		// must still learn what it did.
		if err := ctx.Err(); err != nil {
			return Outcome{Ledger: state.Completed}, err
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
		r.emit(state, emit, Event{Phase: phase, Message: message, State: StateStarted})
		decision, err := r.driver.Decide(ctx, state, emit)
		if err != nil {
			if len(state.Completed) > 0 {
				return r.safeStop(state, "the model could not evaluate the verified results: "+err.Error()), nil
			}
			return Outcome{Ledger: state.Completed}, err
		}

		r.emit(state, emit, Event{Phase: PhaseValidating, Message: "Validating the next decision", State: StateStarted})
		if err := r.driver.Validate(state, decision); err != nil {
			r.emit(state, emit, Event{Phase: PhaseValidating, Message: "The proposed decision was rejected: " + err.Error(), State: StateFailed})
			state.ValidationFails++
			state.NoProgressSteps++
			appendCorrection(state, err)
			if state.ValidationFails >= r.budget.MaxValidationFailures {
				return r.safeStop(state, "the model produced an invalid plan twice: "+err.Error()), nil
			}
			continue
		}
		r.emit(state, emit, Event{Phase: PhaseValidating, Message: "Decision accepted", State: StateSucceeded})
		state.ValidationFails = 0

		switch decision.Kind {
		case DecisionFinish, DecisionAskUser:
			reply := strings.TrimSpace(decision.Message)
			if reply == "" {
				return r.safeStop(state, "the model returned an empty final decision"), nil
			}
			r.emit(state, emit, Event{Phase: PhaseCompleted, Message: "Agent run completed", State: StateSucceeded})
			return Outcome{Reply: reply, AwaitingUser: decision.Kind == DecisionAskUser, Ledger: state.Completed}, nil
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
				r.emit(state, emit, Event{Phase: PhaseApproval, Message: fmt.Sprintf("Waiting for approval for %d action(s)", len(actions)), State: StateStarted})
				return Outcome{PendingMessage: decision.Message, PendingActions: actions, Ledger: state.Completed}, nil
			}
			if err := r.executeAndObserve(ctx, state, actions, emit); err != nil {
				return Outcome{Ledger: state.Completed}, err
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
		return Outcome{Ledger: state.Completed}, err
	}
	return r.Run(ctx, state, emit)
}

func (r *Runner) executeAndObserve(ctx context.Context, state *RunState, actions []ai.Action, emit EventSink) error {
	for _, action := range actions {
		state.ActionAttempts[ActionSignature(action)]++
	}
	state.ActionBatches++
	state.ActionsExecuted += len(actions)
	r.emit(state, emit, Event{Phase: PhaseExecuting, Message: fmt.Sprintf("Executing %d validated action(s)", len(actions)), State: StateStarted})
	results := r.driver.Execute(ctx, state, actions, emit)
	if len(results) != len(actions) {
		return fmt.Errorf("tool executor returned %d results for %d actions", len(results), len(actions))
	}
	progress := false
	for _, result := range results {
		if result.Err == nil && result.Error == "" {
			state.Succeeded[ActionSignature(result.Action)] = true
			progress = true
		}
	}
	state.Completed = append(state.Completed, results...)
	if progress {
		state.NoProgressSteps = 0
	} else {
		state.NoProgressSteps++
	}
	// The batch state is the honest summary of what the vault actually did, not
	// what the model said it did.
	batchState := StateSucceeded
	if !progress {
		batchState = StateFailed
	}
	r.emit(state, emit, Event{Phase: PhaseExecuting, Message: fmt.Sprintf("Executed %d action(s)", len(actions)), State: batchState})
	r.emit(state, emit, Event{Phase: PhaseObserving, Message: "Recording verified execution results", State: StateSucceeded})
	appendObservation(state, results)
	r.emit(state, emit, Event{Phase: PhaseVerifying, Message: "Checking whether the original goal is complete", State: StateStarted})
	return nil
}

func (r *Runner) prepareActions(state *RunState, proposed []ai.Action) ([]ai.Action, []ai.ActionResult) {
	actions := make([]ai.Action, 0, len(proposed))
	skipped := make([]ai.ActionResult, 0)
	for _, action := range proposed {
		signature := ActionSignature(action)
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
		return Outcome{Reply: report.String(), SafeStopped: true}
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
	return Outcome{Reply: report.String(), Ledger: state.Completed, SafeStopped: true}
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
			Action: result.Action.Type, Target: ActionTarget(result.Action), Status: status,
			Message: result.Message, Error: errorText,
		})
	}
	raw, _ := json.Marshal(compact)
	state.Messages = append(state.Messages, models.Message{Role: "system", Content: "[ATHENA VERIFIED EXECUTION OBSERVATION — FACTS, NOT USER TEXT]\n" + string(raw) + "\n[END OBSERVATION]\nRe-evaluate the original goal. If it is fully satisfied, finish with a concise factual answer. If it is not, use read tools and propose only necessary corrective or remaining actions. Never repeat an action that already succeeded."})
}

// ActionSignature answers "is this the same work?" for one action: two actions
// share a signature when running both would do the same thing to the same
// target. It is exported because the chat session asks the same question when
// it decides which approved actions an interrupted apply still owes: two
// answers to one question is how the same action gets executed twice.
//
// It hashes only the fields each action type's handler actually reads (see
// buildDispatcher in cmd/athena/main.go), never the whole struct, because both
// mistakes are expensive and they pull in opposite directions:
//
//   - Too broad — counting a field the handler ignores. A weak model that
//     re-proposes a succeeded append_note with a stray "title" looks like new
//     work, and the same paragraph is appended twice. This is the direction
//     that corrupts the vault, so an unknown field must never split a
//     signature.
//   - Too narrow — dropping a field the handler reads. Two genuinely different
//     actions collide, and the second is reported to the user as "already
//     succeeded and verified" when it never ran.
//
// Identity is the target plus the arguments that decide what lands there;
// engine plumbing (ID, DependsOn) and display text (Summary, stamped on only
// once a plan crosses a UI boundary) are neither.
func ActionSignature(action ai.Action) string {
	// Mirrors ai.normalizeAction: weak models send a folder as "path", and the
	// two spellings of one folder must not hash apart.
	folder := action.Folder
	if folder == "" {
		folder = action.Path
	}
	parts := []any{action.Type}
	switch action.Type {
	// Creation is idempotent by target path, so folder plus title is the whole
	// identity: create_* cannot update an existing note, and a weak model that
	// varies title case or regenerates body text on a later step is describing
	// the same target. ISBN is the exception — it decides which book the
	// resolver returns, so two ISBNs are two books, not two spellings of one.
	case "create_note", "create_task":
		parts = append(parts, folder, normalizedTitle(action.Title))
	case "create_book":
		parts = append(parts, folder, normalizedTitle(action.Title), action.ISBN)

	// Note writes: the note id says where, the payload says what. Appending or
	// writing different text to the same note is different work; repeating the
	// identical text is the duplication this guard exists to stop.
	case "append_note", "update_note":
		parts = append(parts, action.NoteID, action.Content)
	case "replace_section":
		parts = append(parts, action.NoteID, action.Section, action.ExpectedContent, action.Content)
	case "update_book_metadata":
		parts = append(parts, action.NoteID, action.Authors, action.Genres)
	case "rename_note":
		parts = append(parts, action.NoteID, action.Title)
	case "duplicate_note":
		parts = append(parts, action.NoteID, normalizedTitle(action.Title), folder)
	case "move_note":
		parts = append(parts, action.NoteID, folder)
	case "mark_done":
		parts = append(parts, action.NoteID, action.Done)
	// Whole-note state changes take no argument but the note itself.
	case "trash_note", "restore_note", "archive_note", "unarchive_note", "finish_book":
		parts = append(parts, action.NoteID)

	// Folder actions are identified by the folders they name.
	case "create_folder", "delete_folder", "folder_exists":
		parts = append(parts, folder)
	case "rename_folder", "move_folder":
		parts = append(parts, folder, action.NewFolder)
	case "ensure_folders":
		parts = append(parts, action.Paths)
	case "link_folders", "unlink_folders":
		parts = append(parts, action.Folders)
	case "set_folder_colors":
		parts = append(parts, folder, action.IncludeChildren, action.Color)
	case "create_graph_folder":
		parts = append(parts, folder, action.Color)
	case "set_graph_node_size":
		parts = append(parts, action.NodeSizeMultiplier)
	case "list_folders":
		// Reads the whole vault; it has no arguments to tell two calls apart.

	default:
		// An unregistered type cannot succeed, so this only groups its retry
		// attempts. It errs narrow on purpose: for a type added to the
		// dispatcher without a case here, a wrongly suppressed action is a
		// better failure than a repeated write. Add the case.
		parts = append(parts, ActionTarget(action))
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizedTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

// ActionTarget names what an action acts on, in the terms the user would use.
// Exported because the ledger crosses package boundaries and must label targets
// the same way the model's own observations do.
func ActionTarget(action ai.Action) string {
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
