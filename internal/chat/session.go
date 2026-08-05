package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/tools"
)

// Session is the UI-independent owner of one conversation. Callers provide a
// status callback; they decide whether to render it in a CLI, Bubble Tea, or a
// future frontend.
type Session struct {
	mu          sync.Mutex
	loop        *Loop
	history     []models.Message
	pendingPlan *PendingPlan
	nextPlanID  uint64
}

func NewSession(loop *Loop) *Session {
	return &Session{loop: loop, history: []models.Message{{Role: "system", Content: ai.SystemPromptAt(time.Now())}}}
}

func (s *Session) Submit(ctx context.Context, input string, status func(string), onToken func(string)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.submit(ctx, input, status, onToken)
}

func (s *Session) submit(ctx context.Context, input string, status func(string), onToken func(string)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, TurnTimeout)
	defer cancel()

	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	if status != nil {
		status(fmt.Sprintf("Using %s · %s", s.loop.ai.Name(), shortModel(s.loop.ai.ChatModel())))
	}
	if input == "/confirm" {
		return s.confirmPending(ctx)
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
	if actions, ok := folderActions(input); ok {
		if tools.RequiresConfirmation(actions) {
			reply := s.previewActions(actions)
			s.append(input, reply)
			return reply, nil
		}
		reply := s.loop.runActions(ctx, actions)
		s.append(input, reply)
		return reply, nil
	}
	if isListingRequest(input) {
		if status != nil {
			status("Reading vault inventory")
		}
		catalog, err := s.loop.retrieval.Inventory()
		if err != nil {
			return "", fmt.Errorf("list notes: %w", err)
		}
		reply := catalogText(catalog)
		s.append(input, reply)
		return reply, nil
	}
	result, err := s.loop.retrieval.BuildContextWithProgress(ctx, input, 4, status)
	if err != nil {
		if s.loop.ai.Name() == "Ollama" {
			return "", fmt.Errorf("retrieve context: %w", err)
		}
		// Remote chat remains useful when the optional local embedding service
		// is offline. The user sees the status instead of a misleading answer
		// that claims their vault was searched.
		if status != nil {
			status("Vault search is unavailable — answering without vault context")
		}
		result = nil
	}
	s.history = append(s.history, models.Message{Role: "user", Content: input})
	s.compactHistory(false)
	messages := append([]models.Message(nil), s.history...)
	if result != nil && result.Context != "" {
		messages[len(messages)-1].Content = input + "\n\n" + result.Context
	}
	raw, err := s.runReadToolLoop(ctx, messages, status)
	if err != nil {
		s.history = s.history[:len(s.history)-1]
		return "", err
	}
	if result != nil && result.Context != "" && asksUserForVaultInventory(raw) {
		if status != nil {
			status("Model ignored the supplied vault inventory — correcting the plan")
		}
		messages = append(messages,
			models.Message{Role: "assistant", Content: raw},
			models.Message{Role: "user", Content: "Athena already supplied the complete vault inventory and relevant notes above. Do not ask the user to provide them again. Use that data now: give the requested organization plan with valid actions, or state exactly which single classification decision remains ambiguous."},
		)
		raw, err = s.runReadToolLoop(ctx, messages, status)
		if err != nil {
			s.history = s.history[:len(s.history)-1]
			return "", err
		}
	}
	if status != nil {
		status("Writing a response")
	}
	if onToken != nil {
		onToken(raw)
	}
	cleaned, actions := ai.ExtractActions(raw)
	if len(actions) > 0 && tools.RequiresConfirmation(actions) {
		reply := s.previewActions(actions)
		if cleaned != "" {
			reply = cleaned + "\n\n" + reply
		}
		s.history = append(s.history, models.Message{Role: "assistant", Content: reply})
		return reply, nil
	}
	var report strings.Builder
	if cleaned != "" {
		report.WriteString(cleaned)
	}
	if len(actions) > 0 && s.loop.dispatcher != nil {
		if status != nil {
			status(fmt.Sprintf("Executing %d planned action(s)", len(actions)))
		}
		for _, r := range s.loop.dispatcher.RunBatch(ctx, actions, 4) {
			if report.Len() > 0 {
				report.WriteString("\n")
			}
			if r.Err != nil {
				fmt.Fprintf(&report, "Could not %s: %v", r.Action.Type, r.Err)
			} else {
				fmt.Fprintf(&report, "✓ %s", r.Message)
			}
		}
	}
	reply := strings.TrimSpace(report.String())
	if reply == "" {
		reply = "The model returned no visible answer. No vault changes were made; please try again."
	}
	s.history = append(s.history, models.Message{Role: "assistant", Content: reply})
	return reply, nil
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

func (s *Session) previewActions(actions []ai.Action) string {
	s.nextPlanID++
	s.pendingPlan = &PendingPlan{
		ID:        fmt.Sprintf("plan-%d", s.nextPlanID),
		Actions:   append([]ai.Action(nil), actions...),
		CreatedAt: time.Now(),
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
		if action.Section != "" {
			fmt.Fprintf(&out, " section %q", action.Section)
		}
		out.WriteByte('\n')
	}
	out.WriteString("Type /confirm to apply this plan, or /cancel to discard it.")
	return out.String()
}

func (s *Session) confirmPending(ctx context.Context) (string, error) {
	return s.approvePlan(ctx, "")
}

func (s *Session) cancelPending() (string, error) {
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
	return s.approvePlan(ctx, planID)
}

func (s *Session) approvePlan(ctx context.Context, planID string) (string, error) {
	if s.pendingPlan == nil {
		return "There is no pending change to confirm.", nil
	}
	if planID != "" && planID != s.pendingPlan.ID {
		return "", fmt.Errorf("plan %q is no longer pending", planID)
	}
	actions := s.pendingPlan.Actions
	s.pendingPlan = nil
	reply := s.loop.runActions(ctx, actions)
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
		available, err := s.Models(ctx)
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
		available, err := s.Models(ctx)
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
				return s.SelectModel(ctx, model)
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
