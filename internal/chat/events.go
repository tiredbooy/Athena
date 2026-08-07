package chat

import (
	"context"
	"strings"
	"time"

	"github.com/tiredbooy/internal/agent"
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
	RunID    string `json:"run_id,omitempty"`
	Step     int    `json:"step,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Target   string `json:"target,omitempty"`
	State    string `json:"state,omitempty"`
}

// PendingPlan is the engine-owned, one-time approval target for write actions.
// It is copied before crossing a UI or transport boundary.
type PendingPlan struct {
	ID        string      `json:"id"`
	Actions   []ai.Action `json:"actions"`
	CreatedAt time.Time   `json:"createdAt"`
	run       *agent.RunState
	lead      string
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
	s.mu.Lock()
	var beforeID string
	if s.pendingPlan != nil {
		beforeID = s.pendingPlan.ID
	}
	reply, err := s.submit(ctx, input, runObserver{session: s, events: emit}, nil)
	plan := clonePendingPlan(s.pendingPlan)
	s.mu.Unlock()
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
		if plan != nil && plan.ID != beforeID {
			emit(SessionEvent{Type: EventPlanReady, Plan: plan})
		} else {
			emit(SessionEvent{Type: EventResponse, Message: reply})
		}
		emit(SessionEvent{Type: EventCompleted, Message: reply})
	}
	return reply, nil
}

// runObserver keeps legacy status callbacks and structured transport events in
// sync. Agent events bypass text parsing; legacy operations are still adapted
// through activityEvent until their APIs become structured too.
type runObserver struct {
	session *Session
	status  func(string)
	events  EventSink
}

func (o runObserver) statusMessage(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	if o.status != nil {
		o.status(message)
	}
	if o.events != nil {
		activity := &ActivityEvent{Phase: "working", Message: message}
		if o.session != nil {
			activity = o.session.activityEvent(message)
		}
		o.events(SessionEvent{Type: EventActivity, Activity: activity})
	}
}

func (o runObserver) agentSink() agent.EventSink {
	if o.status == nil && o.events == nil {
		return nil
	}
	return func(event agent.Event) {
		if o.status != nil && strings.TrimSpace(event.Message) != "" {
			o.status(event.Message)
		}
		if o.events == nil {
			return
		}
		o.events(SessionEvent{Type: EventActivity, Activity: &ActivityEvent{
			Phase: string(event.Phase), Message: event.Message,
			Provider: event.Provider, Model: event.Model,
			RunID: event.RunID, Step: event.Step, Tool: event.Tool,
			Target: event.Target, State: event.State,
		}})
	}
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
	case strings.HasPrefix(lower, "executing ") ||
		strings.HasPrefix(lower, "creating ") ||
		strings.HasPrefix(lower, "editing ") ||
		strings.HasPrefix(lower, "updating ") ||
		strings.HasPrefix(lower, "moving ") ||
		strings.HasPrefix(lower, "removing ") ||
		strings.HasPrefix(lower, "restoring ") ||
		strings.HasPrefix(lower, "archiving ") ||
		strings.HasPrefix(lower, "unarchiving ") ||
		strings.HasPrefix(lower, "preparing ") ||
		strings.HasPrefix(lower, "linking ") ||
		strings.HasPrefix(lower, "unlinking ") ||
		strings.HasPrefix(lower, "finishing ") ||
		strings.HasPrefix(lower, "running ") ||
		strings.HasPrefix(lower, "could not ") ||
		strings.HasPrefix(lower, "finished ") ||
		strings.HasPrefix(lower, "created ") ||
		strings.HasPrefix(lower, "updated ") ||
		strings.HasPrefix(lower, "moved "):
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
