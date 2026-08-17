package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tiredbooy/internal/agent"
	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

// Session is the UI-independent owner of one conversation. Callers provide a
// status callback; they decide whether to render it in a CLI, Bubble Tea, or a
// future frontend.
//
// It uses two locks because it has two different jobs, and conflating
// them made the engine unresponsive: one turn can run for minutes, and holding
// a single lock across it blocked cancellation, /models, and any UI callback
// that read session state.
//
//   - turnMu serializes whole turns. It is held across model I/O, so nothing
//     that must stay responsive during a turn may take it.
//   - mu guards the mutable state below. Every access holds it briefly and
//     never across a network call.
//
// Lock order, when both are needed: turnMu first, then mu. No path takes mu
// and then turnMu.
type Session struct {
	turnMu sync.Mutex

	mu          sync.Mutex
	loop        *Loop
	history     []models.Message
	pendingPlan *PendingPlan
	pendingTask *PendingTask
	nextPlanID  uint64
	nextRunID   uint64
	// lastLedger is the verified execution record of the most recent turn. A
	// turn returns only its reply text, so the transport reads the structured
	// record from here right after the turn completes, still under turnMu.
	lastLedger []agent.LedgerRecord
	// nativeToolsDisabledModel remembers an Ollama model that rejected the
	// native read-tool schema during this session. The next turn can answer
	// from prepared context instead of paying for another doomed tool request.
	// Only the turn path touches it, so turnMu already serializes it.
	nativeToolsDisabledModel string
}

func NewSession(loop *Loop) *Session {
	return &Session{loop: loop, history: []models.Message{{Role: "system", Content: ai.SystemPromptAt(time.Now())}}}
}

// ModelInfo exposes only the active provider/model label for a UI footer.
// Provider credentials and transport details remain inside the engine.
func (s *Session) ModelInfo() (provider, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loop == nil || s.loop.ai == nil {
		return "", ""
	}
	return s.loop.ai.Name(), s.loop.ai.ChatModel()
}

func (s *Session) Submit(ctx context.Context, input string, status func(string), onToken func(string)) (string, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return s.submit(ctx, input, runObserver{session: s, status: status}, onToken)
}

func (s *Session) submit(ctx context.Context, input string, observer runObserver, onToken func(string)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, TurnTimeout)
	defer cancel()

	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	// E-03: the ledger describes THIS turn. Dropping it as the turn begins is what
	// stops a turn that never reaches the runner — a command, the listing
	// shortcut, a refused input — from republishing the previous turn's record as
	// if it were its own, which is worse than silence.
	s.setLastLedger(nil)
	observer.statusMessage(fmt.Sprintf("Using %s · %s", s.loop.ai.Name(), shortModel(s.loop.ai.ChatModel())))
	if input == "/confirm" {
		return s.approvePlan(ctx, "", observer)
	}
	if input == "/cancel" {
		return s.cancelPending()
	}
	// U-13: review blocks new goals, not inspection. /confirm and /cancel are
	// handled above; the small allowlist below only reads engine state, so the
	// user can still diagnose the engine while deciding on a plan.
	if s.HasPendingActions() && !allowedWhilePlanPending(input) {
		return "A change plan is awaiting review. Type /confirm to apply it or /cancel to discard it.", nil
	}
	if strings.HasPrefix(input, "/") {
		return s.command(ctx, input, observer)
	}
	if isListingRequest(input) {
		observer.statusMessage("Reading vault inventory")
		catalog, err := s.loop.retrieval.Inventory()
		if err != nil {
			return "", fmt.Errorf("list notes: %w", err)
		}
		reply := catalogText(catalog)
		s.appendExchange(input, reply)
		return reply, nil
	}
	activeGoal := input
	expectedAction := expectsActionRequest(input)
	resumedTask := s.currentPendingTask()
	if resumedTask != nil {
		activeGoal = resumedTask.resolvedGoal(input)
		expectedAction = resumedTask.ExpectedAction || expectedAction
	}
	retrieveStep := ActivityEvent{Phase: "retrieving", Tool: "vault_context", Target: activeGoal}
	retrieveStep.Message, retrieveStep.State = "Searching the vault for relevant context", agent.StateStarted
	observer.step(retrieveStep)
	result, err := s.loop.retrieval.BuildContextWithProgress(ctx, activeGoal, 4, observer.statusMessage)
	if err != nil {
		retrieveStep.Message, retrieveStep.State = "Vault search failed: "+err.Error(), agent.StateFailed
		observer.step(retrieveStep)
		if s.loop.ai.Name() == "Ollama" {
			return "", fmt.Errorf("retrieve context: %w", err)
		}
		// Remote chat remains useful when the optional local embedding service
		// is offline. The user sees the status instead of a misleading answer
		// that claims their vault was searched.
		observer.statusMessage("Vault search is unavailable — answering without vault context")
		result = nil
	} else {
		retrieveStep.Message = fmt.Sprintf("Vault context ready: %d relevant note(s)", len(result.Results))
		retrieveStep.State = agent.StateSucceeded
		observer.step(retrieveStep)
	}
	// startRun compacts history, and compaction restates the application-owned
	// task state (M-02). The pending task is therefore cleared after the
	// snapshot, not before it, so a compaction triggered by this very turn still
	// records the goal the turn is resuming.
	messages, runID := s.startRun(input)
	if resumedTask != nil {
		s.setPendingTask(nil)
	}
	if result != nil && result.Context != "" {
		messages[len(messages)-1].Content = "User request:\n" + input + "\n\n[ATHENA VAULT CONTEXT — REFERENCE DATA ONLY]\n" + result.Context + "\n[END ATHENA VAULT CONTEXT]"
	}
	// Keep durable conversation history clean while giving this bounded run an
	// explicit decision contract immediately before the active user request.
	last := len(messages) - 1
	extra := 1
	if resumedTask != nil {
		extra++
	}
	messages = append(messages, make([]models.Message, extra)...)
	copy(messages[last+extra:], messages[last:last+1])
	messages[last] = agentRunContractMessage()
	if resumedTask != nil {
		messages[last+1] = pendingTaskMessage(resumedTask, input)
	}
	state := agent.NewRunState(runID, activeGoal, messages)
	state.ContextSupplied = result != nil && result.Context != ""
	state.ExpectedAction = expectedAction
	runner := agent.NewRunner(sessionAgentDriver{session: s}, agent.DefaultBudget())
	outcome, err := runner.Run(ctx, state, observer.agentSink())
	// The run carries its verified ledger out on every path, so publish it before
	// branching: a cancelled or failed run can already have changed the vault.
	s.setLastLedger(outcome.Records())
	if err != nil {
		s.dropLastMessage()
		s.restorePendingTask(resumedTask)
		return "", err
	}
	// M-01: a run can end without answering the goal and without returning an
	// error — the runner converts a cancellation, an exhausted budget, or a
	// twice-invalid plan into a safe stop. The goal was interrupted, not
	// resolved, so the question the user was replying to must survive it.
	// SafeStopped is the structural signal; the alternative would be matching
	// the "I stopped safely because" prose, which is the English parsing this
	// architecture exists to avoid. finishAgentOutcome still replaces this with
	// a new pending task if the model asked something else first.
	if ctx.Err() != nil || outcome.SafeStopped {
		s.restorePendingTask(resumedTask)
	}
	return s.finishAgentOutcome(state, outcome, onToken, resumedTask, input)
}

// implicitFolderCreationWarning blocks the most damaging weak-model failure:
// turning an uncertain move/link request into new directories. Folder creation
// is allowed when the user's wording explicitly asks for it or names a
// destination for new content; otherwise the user gets a deterministic
// explanation instead of a filesystem mutation.
func implicitFolderCreationWarning(input string, actions []ai.Action) string {
	needsCreation := false
	for _, action := range actions {
		if action.Type == "create_folder" || action.Type == "ensure_folders" {
			needsCreation = true
			break
		}
	}
	if !needsCreation || explicitlyRequestsFolderCreation(input) {
		return ""
	}
	return "I did not create any folders because this request describes an existing-folder operation, but the model proposed creating new paths. Please name the exact existing folders, or explicitly ask me to create the missing folder first."
}

func explicitlyRequestsFolderCreation(input string) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	if explicitlyRequestsContentDestination(input) {
		return true
	}
	if !strings.Contains(input, "folder") && !strings.Contains(input, "directory") {
		return false
	}
	for _, phrase := range []string{
		"create folder", "create a folder", "create the folder",
		"create directory", "create a directory",
		"make folder", "make a folder", "make the folder",
		"make directory", "make a directory",
		"add folder", "add a folder", "add the folder",
		"add directory", "add a directory",
		"new folder", "new folders", "new directory", "new directories",
		"set up folder", "set up a folder", "setup folder", "setup a folder",
		"ensure folder", "ensure folders", "ensure directory", "ensure directories",
		"organize into folders", "organize into directories",
	} {
		if strings.Contains(input, phrase) {
			return true
		}
	}
	return false
}

// A named destination for newly requested content is explicit permission to
// prepare that destination. Books and tasks use the same validated creation
// path as notes, so applying a note-only wording rule rejects legitimate plans.
func explicitlyRequestsContentDestination(input string) bool {
	hasContentKind := containsAny(input, []string{"note", "book", "task"})
	hasCreateIntent := containsAny(input, []string{"create", "add", "make", "start"})
	if !hasContentKind || !hasCreateIntent {
		return false
	}
	for _, phrase := range []string{" in ", " into ", " under ", " inside ", " within "} {
		if strings.Contains(input, phrase) {
			return true
		}
	}
	return false
}

func shortModel(model string) string {
	model = strings.TrimSpace(model)
	if index := strings.LastIndex(model, "/"); index >= 0 {
		model = model[index+1:]
	}
	if len(model) > 44 {
		return model[:43] + "…"
	}
	return model
}

func asksUserForVaultInventory(reply string) bool {
	reply = strings.ToLower(reply)
	return (strings.Contains(reply, "provide") || strings.Contains(reply, "send")) &&
		(strings.Contains(reply, "full list") || strings.Contains(reply, "list of all")) &&
		(strings.Contains(reply, "book") || strings.Contains(reply, "folder") || strings.Contains(reply, "vault"))
}

// withExecutionLedger appends what the vault actually did to the model's
// answer. The model's closing sentence is not evidence: it can be terse, wrong,
// or — on a 2B model — absent. Whenever a run mutated anything, the user sees
// the verified record.
//
// safeStop already writes its own record into the reply, so this does not
// duplicate it.
func withExecutionLedger(reply string, outcome agent.Outcome) string {
	records := outcome.Records()
	if len(records) == 0 || strings.Contains(reply, executionLedgerHeading) {
		return reply
	}
	var out strings.Builder
	out.WriteString(strings.TrimSpace(reply))
	out.WriteString("\n\n")
	out.WriteString(executionLedgerHeading)
	for _, record := range records {
		label := record.Action
		if record.Target != "" {
			label += " " + record.Target
		}
		if record.Status == "failed" {
			detail := record.Error
			if detail == "" {
				detail = "no reason reported"
			}
			fmt.Fprintf(&out, "\n- %s failed: %s", label, detail)
			continue
		}
		if record.Message != "" {
			fmt.Fprintf(&out, "\n- %s succeeded: %s", label, record.Message)
			continue
		}
		fmt.Fprintf(&out, "\n- %s succeeded", label)
	}
	return out.String()
}

const executionLedgerHeading = "Verified execution record:"

func (s *Session) finishAgentOutcome(state *agent.RunState, outcome agent.Outcome, onToken func(string), resumedTask *PendingTask, latestAnswer string) (string, error) {
	var reply string
	if outcome.NeedsApproval() {
		reply = s.previewActions(state, outcome.PendingActions, outcome.PendingMessage)
	} else {
		reply = strings.TrimSpace(outcome.Reply)
		if reply == "" {
			reply = "The agent stopped without a final answer. No unverified success was reported."
		}
		if outcome.AwaitingUser {
			originalGoal := state.Goal
			var answers []string
			if resumedTask != nil {
				originalGoal = resumedTask.OriginalGoal
				answers = append(append([]string(nil), resumedTask.Answers...), strings.TrimSpace(latestAnswer))
			}
			s.setPendingTask(&PendingTask{
				OriginalGoal: originalGoal, Question: reply, Answers: answers,
				ExpectedAction: state.ExpectedAction, CreatedAt: time.Now(),
			})
		}
		reply = withExecutionLedger(reply, outcome)
	}
	s.appendReply(reply)
	if onToken != nil {
		onToken(reply)
	}
	return reply, nil
}

func (s *Session) previewActions(state *agent.RunState, actions []ai.Action, lead string) string {
	actions = describeActions(actions)
	s.mu.Lock()
	s.nextPlanID++
	s.pendingPlan = &PendingPlan{
		ID:        fmt.Sprintf("plan-%d", s.nextPlanID),
		Actions:   append([]ai.Action(nil), actions...),
		CreatedAt: time.Now(),
		run:       state,
		lead:      strings.TrimSpace(lead),
	}
	s.mu.Unlock()

	var out strings.Builder
	out.WriteString("Review required — no changes have been made.\n")
	for _, action := range actions {
		fmt.Fprintf(&out, "• %s", action.Type)
		if action.NoteID != 0 {
			fmt.Fprintf(&out, " (note %d)", action.NoteID)
		}
		if action.Folder != "" {
			fmt.Fprintf(&out, " → %s", action.Folder)
		}
		if len(action.Paths) > 0 {
			fmt.Fprintf(&out, " → %s", strings.Join(action.Paths, ", "))
		}
		if len(action.Folders) > 0 {
			fmt.Fprintf(&out, " ↔ %s", strings.Join(action.Folders, " ↔ "))
		}
		if action.NewFolder != "" {
			fmt.Fprintf(&out, " → parent %s", action.NewFolder)
		} else if action.Type == "move_folder" {
			out.WriteString(" → vault root")
		}
		if action.Section != "" {
			fmt.Fprintf(&out, " section %q", action.Section)
		}
		out.WriteByte('\n')
	}
	out.WriteString("Type /confirm to apply this plan, or /cancel to discard it.")
	return out.String()
}

func (s *Session) confirmPending(ctx context.Context) (string, error) {
	return s.approvePlan(ctx, "", runObserver{session: s})
}

func (s *Session) cancelPending() (string, error) {
	s.mu.Lock()
	discardTask := s.pendingPlan == nil && s.pendingTask != nil
	if discardTask {
		s.pendingTask = nil
	}
	s.mu.Unlock()
	if discardTask {
		reply := "Pending task discarded."
		s.appendExchange("/cancel", reply)
		return reply, nil
	}
	return s.rejectPlan("")
}

// PendingPlan returns a copy of the current plan so a UI cannot mutate the
// engine-owned actions before approval.
func (s *Session) PendingPlan() *PendingPlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clonePendingPlan(s.pendingPlan)
}

// ApprovePlan applies the pending plan once. An empty ID preserves the legacy
// /confirm command; external callers must use the plan ID they received.
func (s *Session) ApprovePlan(ctx context.Context, planID string) (string, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return s.approvePlan(ctx, planID, runObserver{session: s})
}

// ApprovePlanWithEvents applies a plan and reports each factual action state
// through the same event boundary used by normal turns.
func (s *Session) ApprovePlanWithEvents(ctx context.Context, planID string, emit EventSink) (string, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return s.approvePlan(ctx, planID, runObserver{session: s, events: emit})
}

func (s *Session) approvePlan(ctx context.Context, planID string, observer runObserver) (string, error) {
	// Same rule as a normal turn: this approval publishes its own record or none,
	// never the one an earlier turn left behind.
	s.setLastLedger(nil)
	plan, err := s.takePlan(planID)
	if err != nil {
		return "", err
	}
	if plan == nil {
		return "There is no pending change to confirm.", nil
	}
	ctx, cancel := context.WithTimeout(ctx, TurnTimeout)
	defer cancel()

	actions := append([]ai.Action(nil), plan.Actions...)
	if plan.run == nil {
		reply := s.loop.runActionsWithStatus(ctx, actions, observer.statusMessage)
		s.appendExchange("/confirm", reply)
		return reply, nil
	}

	runner := agent.NewRunner(sessionAgentDriver{session: s}, agent.DefaultBudget())
	outcome, err := runner.ResumeApproved(ctx, plan.run, actions, observer.agentSink())
	if err != nil {
		return "", s.restageInterruptedPlan(plan, err)
	}
	var reply string
	if outcome.NeedsApproval() {
		reply = s.previewActions(plan.run, outcome.PendingActions, outcome.PendingMessage)
	} else {
		reply = strings.TrimSpace(outcome.Reply)
		if reply == "" {
			reply = "The approved actions finished, but the agent returned no final summary."
		}
		reply = withExecutionLedger(reply, outcome)
	}
	s.setLastLedger(outcome.Records())
	s.appendExchange("/confirm", reply)
	return reply, nil
}

// restageInterruptedPlan keeps the user's approval alive when applying it was
// cancelled or failed. Approval is the one human decision in the loop; losing
// it to an Escape key or a dropped provider connection makes the user re-read
// and re-approve work they already accepted.
//
// The plan ID stays single-use — this is a NEW plan — and it contains only the
// actions the execution ledger never verified, so a second /confirm cannot
// repeat work that already happened. It carries the same run state, so the
// agent resumes with everything it has already observed.
func (s *Session) restageInterruptedPlan(plan *PendingPlan, cause error) error {
	// The interrupted apply still changed the vault. Publish what it verified,
	// or the user sees only an error for work that partly succeeded.
	s.setLastLedger(agent.Outcome{Ledger: plan.run.Completed}.Records())
	remaining := unverifiedActions(plan.Actions, plan.run.Completed)
	if len(remaining) == 0 {
		return fmt.Errorf("apply approved plan %s: %w", plan.ID, cause)
	}
	// previewActions returns review text for a normal turn; this is an error
	// path, so only the plan it stages matters.
	s.previewActions(plan.run, remaining, "The approved plan was interrupted; only the unverified actions remain.")
	restagedID := "a new pending plan"
	if restaged := s.PendingPlan(); restaged != nil {
		restagedID = restaged.ID
	}
	return fmt.Errorf("apply approved plan %s: %w — %d unverified action(s) are pending review again as %s; /confirm retries only those, /cancel discards them",
		plan.ID, cause, len(remaining), restagedID)
}

// unverifiedActions drops the approved actions the execution ledger already
// recorded as succeeded. "Already succeeded" is agent.ActionSignature — the same
// question the runner asks before it replays an action, so a second /confirm
// cannot disagree with the runner about what is still outstanding.
func unverifiedActions(approved []ai.Action, ledger []ai.ActionResult) []ai.Action {
	succeeded := make(map[string]bool, len(ledger))
	for _, result := range ledger {
		if result.Err == nil && result.Error == "" {
			succeeded[agent.ActionSignature(result.Action)] = true
		}
	}
	remaining := make([]ai.Action, 0, len(approved))
	for _, action := range approved {
		if !succeeded[agent.ActionSignature(action)] {
			remaining = append(remaining, action)
		}
	}
	return remaining
}

// RejectPlan discards the pending plan. Plans cannot be approved after this.
func (s *Session) RejectPlan(planID string) (string, error) {
	return s.rejectPlan(planID)
}

func (s *Session) rejectPlan(planID string) (string, error) {
	plan, err := s.takePlan(planID)
	if err != nil {
		return "", err
	}
	if plan == nil {
		return "There is no pending change to cancel.", nil
	}
	reply := "Pending changes discarded."
	s.appendExchange("/cancel", reply)
	return reply, nil
}

// inspectionCommandsWhilePlanPending is the explicit allowlist of commands that
// may run while a plan awaits review. Each one only reports engine state.
// `/compact` belongs here because a pending plan carries its own resumable run
// state and does not read conversation history, so compacting cannot change
// what `/confirm` would apply. `/model` is deliberately absent: swapping the
// provider under an approved-but-unapplied plan changes who executes it.
var inspectionCommandsWhilePlanPending = map[string]bool{
	"/doctor":  true,
	"/models":  true,
	"/compact": true,
}

func allowedWhilePlanPending(input string) bool {
	fields := strings.Fields(input)
	return len(fields) > 0 && inspectionCommandsWhilePlanPending[fields[0]]
}

// command handles a user command. It takes the observer because a command can
// be long-running: /reindex reports progress through the same status callback a
// turn uses, so the UI does not have to learn a second progress channel.
func (s *Session) command(ctx context.Context, input string, observer runObserver) (string, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", nil
	}
	switch fields[0] {
	case "/compact":
		if s.compact() {
			return "Conversation compacted. Athena retained a short memory plus recent turns.", nil
		}
		return "Conversation is already compact.", nil
	case "/doctor":
		return s.loop.Doctor(ctx), nil
	case "/reindex":
		return s.reindex(ctx, observer)
	case "/models":
		available, err := s.loop.Models(ctx)
		if err != nil {
			return "", fmt.Errorf("list models: %w", err)
		}
		if len(available) == 0 {
			return "No chat-capable Ollama models found.", nil
		}
		var out strings.Builder
		out.WriteString("Available chat models:")
		for i, model := range available {
			marker := " "
			if model.Current {
				marker = "*"
			}
			fmt.Fprintf(&out, "\n%s %d. %s", marker, i+1, model.Model)
		}
		return out.String(), nil
	case "/model":
		if len(fields) != 2 {
			return "Usage: /model <number-or-name>", nil
		}
		available, err := s.loop.Models(ctx)
		if err != nil {
			return "", fmt.Errorf("list models: %w", err)
		}
		selected := fields[1]
		if index, err := strconv.Atoi(selected); err == nil {
			if index < 1 || index > len(available) {
				return "Model number is out of range. Run /models first.", nil
			}
			selected = available[index-1].Model
		}
		for _, model := range available {
			if model.Model == selected {
				return s.loop.SelectModel(ctx, model.ProviderID, model.Model)
			}
		}
		return fmt.Sprintf("Model %q is not available. Run /models first.", selected), nil
	default:
		return "Unknown command. Type /help to see available commands.", nil
	}
}

// reindex rebuilds every vector in the vault (V-03).
//
// It is a user command and deliberately not an action type. Re-embedding a
// whole vault is the most expensive thing Athena can do — one embedding request
// per chunk of every note, trashed ones included — so the decision to spend
// that stays with the user. A 2B local model that proposed it on a whim would
// be spending minutes of the user's hardware on a guess.
//
// ponytail: it runs inside the ordinary TurnTimeout, so a vault too large to
// rebuild in five minutes cannot be rebuilt this way. Give the job its own
// deadline (or run it in the background against the jobs table it already
// writes) when a vault that big turns up.
func (s *Session) reindex(ctx context.Context, observer runObserver) (string, error) {
	if s.loop.notes == nil {
		return "I can't rebuild the index because the vault service is unavailable.", nil
	}
	observer.statusMessage("Rebuilding vault vectors")
	rebuilt := 0
	// Progress reports notes, not chunks: it is what the user can count.
	if err := s.loop.notes.Reindex(ctx, func(current, total int) {
		rebuilt = current
		observer.statusMessage(fmt.Sprintf("Rebuilding vault vectors: note %d of %d", current, total))
	}); err != nil {
		return "", fmt.Errorf("rebuild vault index: %w", err)
	}
	return fmt.Sprintf("Rebuilt the vault index: %d note(s) re-embedded with the configured embedding model. Run /doctor to confirm.", rebuilt), nil
}

// Reset returns the session to its opening state: the system prompt only, no
// pending plan, no pending clarification task. A UI clearing its own transcript
// is not this — that leaves the engine remembering a conversation the user
// believes is gone. Clients ask for this explicitly via `session.reset`.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = s.history[:1]
	s.pendingPlan = nil
	s.pendingTask = nil
}

// HasPendingActions is the UI contract for whether its approval controls are
// valid. Keeping this state in Session avoids inferring it from model prose.
func (s *Session) HasPendingActions() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingPlan != nil
}

// append records one exchange. The caller must hold mu.
func (s *Session) append(input, reply string) {
	s.history = append(s.history, models.Message{Role: "user", Content: input}, models.Message{Role: "assistant", Content: reply})
}

// The helpers below are the only way the turn path touches session state. Each
// takes mu for exactly one operation, so a turn that runs for minutes never
// makes /models, a reset, or a UI callback wait on generation.

func (s *Session) appendExchange(input, reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.append(input, reply)
}

func (s *Session) appendReply(reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, models.Message{Role: "assistant", Content: reply})
}

func (s *Session) setPendingTask(task *PendingTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingTask = task
}

// restorePendingTask puts an interrupted clarification back (M-01). Only an
// explicit /cancel, a reset, or a goal the run actually resolved may drop a
// pending task; a failure, a cancellation, or a timeout must not cost the user
// the question they were in the middle of answering.
func (s *Session) restorePendingTask(task *PendingTask) {
	if task == nil {
		return
	}
	s.setPendingTask(task)
}

func (s *Session) setLastLedger(records []agent.LedgerRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastLedger = records
}

// LastLedger returns the verified execution record of the most recent turn.
func (s *Session) LastLedger() []agent.LedgerRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agent.LedgerRecord(nil), s.lastLedger...)
}

func (s *Session) currentPendingTask() *PendingTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingTask
}

// startRun appends the user message, compacts, and snapshots the prompt in one
// critical section so history cannot change between those steps.
func (s *Session) startRun(input string) (messages []models.Message, runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, models.Message{Role: "user", Content: input})
	s.compactHistory(false)
	s.nextRunID++
	return append([]models.Message(nil), s.history...), fmt.Sprintf("run-%d", s.nextRunID)
}

// dropLastMessage undoes startRun's append when the run failed. It never
// removes the system prompt, which Reset and compaction both rely on.
func (s *Session) dropLastMessage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.history) > 1 {
		s.history = s.history[:len(s.history)-1]
	}
}

// takePlan claims the pending plan so it can be applied or discarded exactly
// once. A plan ID is single-use: a second approval finds nothing pending.
func (s *Session) takePlan(planID string) (*PendingPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingPlan == nil {
		return nil, nil
	}
	if planID != "" && planID != s.pendingPlan.ID {
		return nil, fmt.Errorf("plan %q is no longer pending", planID)
	}
	plan := s.pendingPlan
	s.pendingPlan = nil
	return plan, nil
}

func (s *Session) compact() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compactHistory(true)
}
