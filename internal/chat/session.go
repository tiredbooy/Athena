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
type Session struct {
	mu          sync.Mutex
	loop        *Loop
	history     []models.Message
	pendingPlan *PendingPlan
	pendingTask *PendingTask
	nextPlanID  uint64
	nextRunID   uint64
	// nativeToolsDisabledModel remembers an Ollama model that rejected the
	// native read-tool schema during this session. The next turn can answer
	// from prepared context instead of paying for another doomed tool request.
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.submit(ctx, input, runObserver{session: s, status: status}, onToken)
}

func (s *Session) submit(ctx context.Context, input string, observer runObserver, onToken func(string)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, TurnTimeout)
	defer cancel()

	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	observer.statusMessage(fmt.Sprintf("Using %s · %s", s.loop.ai.Name(), shortModel(s.loop.ai.ChatModel())))
	if input == "/confirm" {
		return s.approvePlan(ctx, "", observer)
	}
	if input == "/cancel" {
		return s.cancelPending()
	}
	if s.pendingPlan != nil {
		return "A change plan is awaiting review. Type /confirm to apply it or /cancel to discard it.", nil
	}
	if strings.HasPrefix(input, "/") {
		return s.command(ctx, input)
	}
	if isListingRequest(input) {
		observer.statusMessage("Reading vault inventory")
		catalog, err := s.loop.retrieval.Inventory()
		if err != nil {
			return "", fmt.Errorf("list notes: %w", err)
		}
		reply := catalogText(catalog)
		s.append(input, reply)
		return reply, nil
	}
	activeGoal := input
	expectedAction := expectsActionRequest(input)
	resumedTask := s.pendingTask
	if resumedTask != nil {
		activeGoal = resumedTask.resolvedGoal(input)
		expectedAction = resumedTask.ExpectedAction || expectedAction
	}
	result, err := s.loop.retrieval.BuildContextWithProgress(ctx, activeGoal, 4, observer.statusMessage)
	if err != nil {
		if s.loop.ai.Name() == "Ollama" {
			return "", fmt.Errorf("retrieve context: %w", err)
		}
		// Remote chat remains useful when the optional local embedding service
		// is offline. The user sees the status instead of a misleading answer
		// that claims their vault was searched.
		observer.statusMessage("Vault search is unavailable — answering without vault context")
		result = nil
	}
	if resumedTask != nil {
		s.pendingTask = nil
	}
	s.history = append(s.history, models.Message{Role: "user", Content: input})
	s.compactHistory(false)
	messages := append([]models.Message(nil), s.history...)
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
	s.nextRunID++
	state := agent.NewRunState(fmt.Sprintf("run-%d", s.nextRunID), activeGoal, messages)
	state.ContextSupplied = result != nil && result.Context != ""
	state.ExpectedAction = expectedAction
	runner := agent.NewRunner(sessionAgentDriver{session: s}, agent.DefaultBudget())
	outcome, err := runner.Run(ctx, state, observer.agentSink())
	if err != nil {
		s.history = s.history[:len(s.history)-1]
		if resumedTask != nil {
			s.pendingTask = resumedTask
		}
		return "", err
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
			s.pendingTask = &PendingTask{
				OriginalGoal: originalGoal, Question: reply, Answers: answers,
				ExpectedAction: state.ExpectedAction, CreatedAt: time.Now(),
			}
		}
	}
	s.history = append(s.history, models.Message{Role: "assistant", Content: reply})
	if onToken != nil {
		onToken(reply)
	}
	return reply, nil
}

func (s *Session) previewActions(state *agent.RunState, actions []ai.Action, lead string) string {
	s.nextPlanID++
	s.pendingPlan = &PendingPlan{
		ID:        fmt.Sprintf("plan-%d", s.nextPlanID),
		Actions:   append([]ai.Action(nil), actions...),
		CreatedAt: time.Now(),
		run:       state,
		lead:      strings.TrimSpace(lead),
	}
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
	if s.pendingPlan == nil && s.pendingTask != nil {
		s.pendingTask = nil
		reply := "Pending task discarded."
		s.append("/cancel", reply)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvePlan(ctx, planID, runObserver{session: s})
}

// ApprovePlanWithEvents applies a plan and reports each factual action state
// through the same event boundary used by normal turns.
func (s *Session) ApprovePlanWithEvents(ctx context.Context, planID string, emit EventSink) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvePlan(ctx, planID, runObserver{session: s, events: emit})
}

func (s *Session) approvePlan(ctx context.Context, planID string, observer runObserver) (string, error) {
	if s.pendingPlan == nil {
		return "There is no pending change to confirm.", nil
	}
	if planID != "" && planID != s.pendingPlan.ID {
		return "", fmt.Errorf("plan %q is no longer pending", planID)
	}
	ctx, cancel := context.WithTimeout(ctx, TurnTimeout)
	defer cancel()

	plan := s.pendingPlan
	actions := append([]ai.Action(nil), plan.Actions...)
	s.pendingPlan = nil
	if plan.run == nil {
		reply := s.loop.runActionsWithStatus(ctx, actions, observer.statusMessage)
		s.append("/confirm", reply)
		return reply, nil
	}

	runner := agent.NewRunner(sessionAgentDriver{session: s}, agent.DefaultBudget())
	outcome, err := runner.ResumeApproved(ctx, plan.run, actions, observer.agentSink())
	if err != nil {
		return "", err
	}
	var reply string
	if outcome.NeedsApproval() {
		reply = s.previewActions(plan.run, outcome.PendingActions, outcome.PendingMessage)
	} else {
		reply = strings.TrimSpace(outcome.Reply)
		if reply == "" {
			reply = "The approved actions finished, but the agent returned no final summary."
		}
	}
	s.append("/confirm", reply)
	return reply, nil
}

// RejectPlan discards the pending plan. Plans cannot be approved after this.
func (s *Session) RejectPlan(planID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rejectPlan(planID)
}

func (s *Session) rejectPlan(planID string) (string, error) {
	if s.pendingPlan == nil {
		return "There is no pending change to cancel.", nil
	}
	if planID != "" && planID != s.pendingPlan.ID {
		return "", fmt.Errorf("plan %q is no longer pending", planID)
	}
	s.pendingPlan = nil
	reply := "Pending changes discarded."
	s.append("/cancel", reply)
	return reply, nil
}

func (s *Session) command(ctx context.Context, input string) (string, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", nil
	}
	switch fields[0] {
	case "/compact":
		if s.compactHistory(true) {
			return "Conversation compacted. Athena retained a short memory plus recent turns.", nil
		}
		return "Conversation is already compact.", nil
	case "/doctor":
		return s.loop.Doctor(ctx), nil
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

func (s *Session) Clear() {
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

func (s *Session) append(input, reply string) {
	s.history = append(s.history, models.Message{Role: "user", Content: input}, models.Message{Role: "assistant", Content: reply})
}
