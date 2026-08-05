package chat

import (
	"context"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
)

// EventType describes a session event without prescribing how a terminal
// renders it. Transport adapters add request and turn identifiers around these
// events.
type EventType string

const (
	EventActivity  EventType = "activity"
	EventResponse  EventType = "response"
	EventPlanReady EventType = "plan.ready"
	EventCompleted EventType = "completed"
	EventError     EventType = "error"
	EventCancelled EventType = "cancelled"
)

// ActivityEvent is a factual operation performed by Athena. It intentionally
// has no model reasoning field: UIs may show actual work, not guessed thought.
type ActivityEvent struct {
	Phase    string `json:"phase"`
	Message  string `json:"message"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Path     string `json:"path,omitempty"`
}

// PendingPlan is the engine-owned, one-time approval target for write actions.
// It is copied before crossing a UI or transport boundary.
type PendingPlan struct {
	ID        string      `json:"id"`
	Actions   []ai.Action `json:"actions"`
	CreatedAt time.Time   `json:"createdAt"`
}

// SessionEvent is the stable application-facing event contract. The existing
// Bubble Tea UI consumes the legacy callback adapter while a future UI can
// consume this structure directly.
type SessionEvent struct {
	Type     EventType      `json:"type"`
	Message  string         `json:"message,omitempty"`
	Activity *ActivityEvent `json:"activity,omitempty"`
	Plan     *PendingPlan   `json:"plan,omitempty"`
	Error    string         `json:"error,omitempty"`
}

type EventSink func(SessionEvent)

// SubmitWithEvents adapts the current session behavior into structured events.
// It is deliberately additive so the legacy UI keeps its established contract
// while the new stdio engine is introduced.
func (s *Session) SubmitWithEvents(ctx context.Context, input string, emit EventSink) (string, error) {
	var beforeID string
	if plan := s.PendingPlan(); plan != nil {
		beforeID = plan.ID
	}

	reply, err := s.Submit(ctx, input, func(message string) {
		if emit != nil {
			emit(SessionEvent{Type: EventActivity, Activity: s.activityEvent(message)})
		}
	}, nil)
	if err != nil {
		if emit != nil {
			if ctx.Err() != nil {
				emit(SessionEvent{Type: EventCancelled, Message: "The request was cancelled."})
			} else {
				emit(SessionEvent{Type: EventError, Error: err.Error()})
			}
		}
		return "", err
	}

	if emit != nil {
		if plan := s.PendingPlan(); plan != nil && plan.ID != beforeID {
			emit(SessionEvent{Type: EventPlanReady, Plan: plan})
		} else {
			emit(SessionEvent{Type: EventResponse, Message: reply})
		}
		emit(SessionEvent{Type: EventCompleted, Message: reply})
	}
	return reply, nil
}

func (s *Session) activityEvent(message string) *ActivityEvent {
	activity := &ActivityEvent{Phase: "working", Message: message}
	lower := strings.ToLower(message)
	switch {
	case strings.HasPrefix(lower, "reading "):
		activity.Phase = "reading"
		if path := strings.TrimSpace(message[len("Reading "):]); isVaultRelativePath(path) {
			activity.Path = path
		}
	case strings.HasPrefix(lower, "searching "):
		activity.Phase = "searching"
	case strings.HasPrefix(lower, "embedding "):
		activity.Phase = "embedding"
	case strings.Contains(lower, "generating") || strings.HasPrefix(lower, "using ") || strings.HasPrefix(lower, "retrying the model"):
		activity.Phase = "provider_wait"
		activity.Provider = s.loop.ai.Name()
		activity.Model = s.loop.ai.ChatModel()
	case strings.HasPrefix(lower, "executing "):
		activity.Phase = "executing"
	}
	return activity
}

func isVaultRelativePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "../") {
		return false
	}
	return strings.Contains(path, "/") || strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".txt")
}

func clonePendingPlan(plan *PendingPlan) *PendingPlan {
	if plan == nil {
		return nil
	}
	copy := *plan
	copy.Actions = append([]ai.Action(nil), plan.Actions...)
	return &copy
}
