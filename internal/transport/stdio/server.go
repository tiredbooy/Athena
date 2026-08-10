// Package stdio exposes Athena's local engine over a versioned JSON-lines
// protocol. It is an outer transport adapter: conversation and vault policy
// remain in the chat and tools packages.
package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/chat"
)

const ProtocolVersion = 1

const (
	RequestHello           = "engine.hello"
	RequestSubmit          = "session.submit"
	RequestCancel          = "session.cancel"
	RequestPlanApprove     = "plan.approve"
	RequestPlanReject      = "plan.reject"
	RequestModelList       = "model.list"
	RequestModelSelect     = "model.select"
	RequestProviderList    = "provider.list"
	RequestProviderConnect = "provider.connect"
	RequestProviderOAuth   = "provider.oauth.start"
)

// Request is one newline-delimited message from a user interface.
type Request struct {
	Version    int                   `json:"version"`
	RequestID  string                `json:"requestId"`
	Type       string                `json:"type"`
	Input      string                `json:"input,omitempty"`
	TurnID     string                `json:"turnId,omitempty"`
	PlanID     string                `json:"planId,omitempty"`
	ProviderID string                `json:"providerId,omitempty"`
	Model      string                `json:"model,omitempty"`
	Connection *chat.ConnectionInput `json:"connection,omitempty"`
}

// Event is one newline-delimited message written by the engine. Standard
// output contains only these messages; diagnostics belong on standard error.
type Event struct {
	Version   int                   `json:"version"`
	RequestID string                `json:"requestId,omitempty"`
	Type      string                `json:"type"`
	TurnID    string                `json:"turnId,omitempty"`
	PlanID    string                `json:"planId,omitempty"`
	Message   string                `json:"message,omitempty"`
	Error     string                `json:"error,omitempty"`
	Provider  string                `json:"provider,omitempty"`
	Model     string                `json:"model,omitempty"`
	Activity  *chat.ActivityEvent   `json:"activity,omitempty"`
	Actions   []ai.Action           `json:"actions,omitempty"`
	Models    []chat.ModelOption    `json:"models,omitempty"`
	Presets   []chat.ProviderPreset `json:"presets,omitempty"`
}

// Server serializes one chat session and lets a client cancel its active turn.
// A second submit is rejected rather than queued invisibly, so the UI can keep
// its composer responsive and make the state clear to the user.
type Server struct {
	ctx     context.Context
	session *chat.Session
	output  *json.Encoder

	writeMu  sync.Mutex
	stateMu  sync.Mutex
	turns    map[string]context.CancelFunc
	active   string
	busy     bool
	work     sync.WaitGroup
	writeErr error
}

func Serve(ctx context.Context, input io.Reader, output io.Writer, session *chat.Session) error {
	if session == nil {
		return fmt.Errorf("stdio engine requires a chat session")
	}
	server := &Server{
		ctx:     ctx,
		session: session,
		output:  json.NewEncoder(output),
		turns:   make(map[string]context.CancelFunc),
	}
	return server.serve(input)
}

func (s *Server) serve(input io.Reader) error {
	scanner := bufio.NewScanner(input)
	// A vault request can legitimately contain a large pasted prompt. Scanner's
	// default 64 KiB token would turn that into a confusing transport failure.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request Request
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			s.emit(Event{Type: "error", Error: "invalid JSON request: " + err.Error()})
			continue
		}
		if err := validate(request); err != nil {
			s.emit(Event{RequestID: request.RequestID, Type: "error", Error: err.Error()})
			continue
		}
		s.handle(request)
	}
	s.work.Wait()
	if err := scanner.Err(); err != nil {
		return err
	}
	return s.outputError()
}

func validate(request Request) error {
	if request.Version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %d; expected %d", request.Version, ProtocolVersion)
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return fmt.Errorf("requestId is required")
	}
	if strings.TrimSpace(request.Type) == "" {
		return fmt.Errorf("type is required")
	}
	switch request.Type {
	case RequestSubmit:
		if strings.TrimSpace(request.Input) == "" {
			return fmt.Errorf("input is required for %s", RequestSubmit)
		}
	case RequestCancel:
		if strings.TrimSpace(request.TurnID) == "" {
			return fmt.Errorf("turnId is required for %s", RequestCancel)
		}
	case RequestPlanApprove, RequestPlanReject:
		if strings.TrimSpace(request.PlanID) == "" {
			return fmt.Errorf("planId is required for %s", request.Type)
		}
	case RequestModelSelect:
		if strings.TrimSpace(request.ProviderID) == "" || strings.TrimSpace(request.Model) == "" {
			return fmt.Errorf("providerId and model are required for %s", request.Type)
		}
	case RequestProviderConnect:
		if request.Connection == nil {
			return fmt.Errorf("connection is required for %s", request.Type)
		}
	case RequestProviderOAuth:
		if strings.TrimSpace(request.ProviderID) == "" {
			return fmt.Errorf("providerId is required for %s", request.Type)
		}
	}
	return nil
}

func (s *Server) handle(request Request) {
	switch request.Type {
	case RequestHello:
		provider, model := s.session.ModelInfo()
		s.emit(Event{RequestID: request.RequestID, Type: "engine.ready", Message: "Athena engine is ready", Provider: provider, Model: model})
	case RequestSubmit:
		s.startSubmit(request)
	case RequestCancel:
		s.cancelTurn(request)
	case RequestPlanApprove:
		s.startPlanApproval(request, true)
	case RequestPlanReject:
		s.startPlanApproval(request, false)
	case RequestModelList:
		models, err := s.session.Models(s.ctx)
		if err != nil {
			s.emit(Event{RequestID: request.RequestID, Type: "error", Error: err.Error()})
			return
		}
		s.emit(Event{RequestID: request.RequestID, Type: "model.options", Models: models})
	case RequestModelSelect:
		message, err := s.session.SelectModel(s.ctx, chat.ModelOption{ProviderID: request.ProviderID, Model: request.Model})
		if err != nil {
			s.emit(Event{RequestID: request.RequestID, Type: "error", Error: err.Error()})
			return
		}
		provider, model := s.session.ModelInfo()
		s.emit(Event{RequestID: request.RequestID, Type: "model.selected", Message: message, Provider: provider, Model: model})
	case RequestProviderList:
		s.emit(Event{RequestID: request.RequestID, Type: "provider.presets", Presets: s.session.ProviderPresets()})
	case RequestProviderConnect:
		message, err := s.session.Connect(*request.Connection)
		if err != nil {
			s.emit(Event{RequestID: request.RequestID, Type: "error", Error: err.Error()})
			return
		}
		provider, model := s.session.ModelInfo()
		s.emit(Event{RequestID: request.RequestID, Type: "provider.connected", Message: message, Provider: provider, Model: model})
	case RequestProviderOAuth:
		s.startProviderOAuth(request)
	default:
		s.emit(Event{RequestID: request.RequestID, Type: "error", Error: fmt.Sprintf("unsupported request type %q", request.Type)})
	}
}

func (s *Server) startProviderOAuth(request Request) {
	if request.ProviderID != "openai-codex" && request.ProviderID != "xai-oauth" {
		s.emit(Event{RequestID: request.RequestID, Type: "error", Error: fmt.Sprintf("OAuth is unavailable for provider %q", request.ProviderID)})
		return
	}
	turnID := "oauth-" + request.RequestID
	s.stateMu.Lock()
	if s.busy {
		s.stateMu.Unlock()
		s.emit(Event{RequestID: request.RequestID, Type: "error", Error: "another operation is already running"})
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.busy, s.active, s.turns[turnID] = true, turnID, cancel
	s.stateMu.Unlock()

	s.emit(Event{Type: "provider.oauth.started", TurnID: turnID, Provider: request.ProviderID, Message: "Starting provider device login"})
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		defer s.finishTurn(turnID)
		message, err := s.session.ConnectOAuth(ctx, request.ProviderID, func(line string) {
			s.emit(Event{Type: "provider.oauth.progress", TurnID: turnID, Message: line})
		})
		if err != nil {
			if ctx.Err() != nil {
				s.emit(Event{RequestID: request.RequestID, Type: "provider.oauth.cancelled", TurnID: turnID, Message: "Sign-in cancelled"})
				return
			}
			s.emit(Event{RequestID: request.RequestID, Type: "error", TurnID: turnID, Error: err.Error()})
			return
		}
		provider, model := s.session.ModelInfo()
		s.emit(Event{RequestID: request.RequestID, Type: "provider.connected", TurnID: turnID, Message: message, Provider: provider, Model: model})
	}()
}

func (s *Server) startSubmit(request Request) {
	turnID := strings.TrimSpace(request.TurnID)
	if turnID == "" {
		turnID = "turn-" + request.RequestID
	}

	s.stateMu.Lock()
	if s.busy {
		s.stateMu.Unlock()
		s.emit(Event{RequestID: request.RequestID, Type: "error", TurnID: turnID, Error: "another turn is already running; cancel it or wait for completion"})
		return
	}
	turnCtx, cancel := context.WithCancel(s.ctx)
	s.active = turnID
	s.busy = true
	s.turns[turnID] = cancel
	s.stateMu.Unlock()

	s.work.Add(1)
	go func() {
		defer s.work.Done()
		defer s.finishTurn(turnID)
		s.emit(Event{RequestID: request.RequestID, Type: "turn.started", TurnID: turnID, Message: "Turn started"})
		_, err := s.session.SubmitWithEvents(turnCtx, request.Input, func(event chat.SessionEvent) {
			s.forwardSessionEvent(request.RequestID, turnID, event)
		})
		if err != nil && turnCtx.Err() == nil {
			// SubmitWithEvents already sends the detailed error. This final state
			// lets clients stop their spinners without parsing prose.
			s.emit(Event{RequestID: request.RequestID, Type: "turn.failed", TurnID: turnID, Error: err.Error()})
		}
	}()
}

func (s *Server) cancelTurn(request Request) {
	turnID := strings.TrimSpace(request.TurnID)
	if turnID == "" {
		s.emit(Event{RequestID: request.RequestID, Type: "error", Error: "turnId is required to cancel a turn"})
		return
	}
	s.stateMu.Lock()
	cancel, ok := s.turns[turnID]
	s.stateMu.Unlock()
	if !ok {
		s.emit(Event{RequestID: request.RequestID, Type: "error", TurnID: turnID, Error: "turn is not running"})
		return
	}
	cancel()
	s.emit(Event{RequestID: request.RequestID, Type: "turn.cancellation_requested", TurnID: turnID, Message: "Cancellation requested"})
}

func (s *Server) startPlanApproval(request Request, approve bool) {
	if strings.TrimSpace(request.PlanID) == "" {
		s.emit(Event{RequestID: request.RequestID, Type: "error", Error: "planId is required"})
		return
	}
	s.stateMu.Lock()
	busy := s.busy
	if !busy {
		s.busy = true
	}
	s.stateMu.Unlock()
	if busy {
		s.emit(Event{RequestID: request.RequestID, Type: "error", PlanID: request.PlanID, Error: "a turn is still running; wait before deciding on its plan"})
		return
	}

	s.work.Add(1)
	go func() {
		defer s.work.Done()
		defer s.finishPlanOperation()
		var (
			message string
			err     error
		)
		if approve {
			message, err = s.session.ApprovePlanWithEvents(s.ctx, request.PlanID, func(event chat.SessionEvent) {
				s.forwardSessionEvent(request.RequestID, "", event)
			})
		} else {
			message, err = s.session.RejectPlan(request.PlanID)
		}
		if err != nil {
			s.emit(Event{RequestID: request.RequestID, Type: "error", PlanID: request.PlanID, Error: err.Error()})
			return
		}
		nextPlan := s.session.PendingPlan()
		typeName := "plan.rejected"
		if approve {
			typeName = "plan.approved"
		}
		s.emit(Event{RequestID: request.RequestID, Type: typeName, PlanID: request.PlanID, Message: message})
		// Approval resumes the same agent run. If execution observations reveal
		// another genuinely necessary high-risk step, expose a fresh one-time
		// plan instead of hiding it inside the approval response.
		if approve && nextPlan != nil && nextPlan.ID != request.PlanID {
			s.emit(Event{
				RequestID: request.RequestID, Type: "plan.ready", PlanID: nextPlan.ID,
				Actions: nextPlan.Actions,
				Message: fmt.Sprintf("%d additional change(s) are ready for review", len(nextPlan.Actions)),
			})
		}
	}()
}

func (s *Server) forwardSessionEvent(requestID, turnID string, event chat.SessionEvent) {
	typeName := string(event.Type)
	switch event.Type {
	case chat.EventCompleted, chat.EventCancelled, chat.EventError:
		typeName = "turn." + string(event.Type)
	}
	output := Event{
		RequestID: requestID,
		TurnID:    turnID,
		Type:      typeName,
		Message:   event.Message,
		Error:     event.Error,
		Activity:  event.Activity,
	}
	if event.Plan != nil {
		output.PlanID = event.Plan.ID
		output.Actions = event.Plan.Actions
		output.Message = fmt.Sprintf("%d change(s) are ready for review", len(event.Plan.Actions))
	}
	s.emit(output)
}

func (s *Server) finishTurn(turnID string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	delete(s.turns, turnID)
	if s.active == turnID {
		s.active = ""
	}
	s.busy = false
}

func (s *Server) finishPlanOperation() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.busy = false
}

func (s *Server) emit(event Event) {
	event.Version = ProtocolVersion
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeErr != nil {
		return
	}
	if err := s.output.Encode(event); err != nil {
		s.writeErr = fmt.Errorf("write engine event: %w", err)
	}
}

func (s *Server) outputError() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writeErr
}
