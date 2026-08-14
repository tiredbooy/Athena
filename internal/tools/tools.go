package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

type Handler func(ctx context.Context, a ai.Action) (string, error)

// ActionProgress is a factual lifecycle update for one dispatched action.
// It is carried through context so concurrent batches can report progress
// without sharing mutable observer state on Dispatcher.
type ActionProgress struct {
	Action  ai.Action
	State   string
	Message string
	Error   error
}

type actionProgressKey struct{}

// WithActionProgress attaches a per-operation observer to a context. The
// observer is optional; the dispatcher remains useful for callers that only
// need final ActionResult values.
func WithActionProgress(ctx context.Context, observer func(ActionProgress)) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, actionProgressKey{}, observer)
}

func reportActionProgress(ctx context.Context, progress ActionProgress) {
	observer, ok := ctx.Value(actionProgressKey{}).(func(ActionProgress))
	if ok && observer != nil {
		observer(progress)
	}
}

type Dispatcher struct {
	handlers  map[string]Handler
	verifiers map[string]Verifier
	auditor   AuditLogger
}

// AuditLogger is implemented by infrastructure storage. Keeping the small
// interface here lets the application dispatcher own the reliability policy
// without depending on SQLite directly.
type AuditLogger interface {
	Record(context.Context, models.ActionAudit) error
}

// Verifier re-reads durable state after a successful write. It must not make
// changes itself; its only job is to prevent an unverified success report.
type Verifier func(context.Context, ai.Action) error

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]Handler), verifiers: make(map[string]Verifier)}
}

func (d *Dispatcher) Register(actionType string, h Handler) {
	d.handlers[actionType] = h
}

func (d *Dispatcher) SetAuditLogger(auditor AuditLogger) {
	d.auditor = auditor
}

// Validate checks a model-generated plan without running handlers. The chat
// layer uses this before showing an approval card so malformed plans can be
// repaired by the model instead of making the user approve a doomed action.
func (d *Dispatcher) Validate(actions []ai.Action) error {
	if len(actions) > maxActionsPerBatch {
		return fmt.Errorf("action batch exceeds limit of %d", maxActionsPerBatch)
	}
	for index, action := range actions {
		action.Type = strings.TrimSpace(action.Type)
		if err := validateAction(action, d.handlers[action.Type] != nil); err != nil {
			return fmt.Errorf("action %d (%s): %w", index+1, action.Type, err)
		}
	}
	return nil
}

// RecordRejectedPlan preserves model proposals that failed preflight. They do
// not reach Run and therefore would otherwise be absent from action_audit,
// making repeated schema failures impossible to diagnose after the session.
func (d *Dispatcher) RecordRejectedPlan(actions []ai.Action, validationErr error) {
	if d.auditor == nil || validationErr == nil {
		return
	}
	for _, action := range actions {
		payload, err := json.Marshal(action)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = d.auditor.Record(ctx, models.ActionAudit{
			ActionType: action.Type, ActionJSON: string(payload), Outcome: "rejected",
			Error: validationErr.Error(), CreatedAt: time.Now(),
		})
		cancel()
	}
}

func (d *Dispatcher) RegisterVerifier(actionType string, verifier Verifier) {
	d.verifiers[actionType] = verifier
}

func (d *Dispatcher) Run(ctx context.Context, actions []ai.Action) []ai.ActionResult {
	if len(actions) > maxActionsPerBatch {
		return d.rejectOversizedBatch(ctx, actions)
	}
	results := make([]ai.ActionResult, 0, len(actions))
	for _, a := range actions {
		results = append(results, d.runAction(ctx, a))
	}
	return results
}

// RunBatch executes a dependency-aware action plan. It preserves the input
// order in its results so every UI can render a stable report. A plan is run
// concurrently only when each action has a unique ID; this lets weaker models
// keep emitting the original simple action format safely.
//
// A failed action does not stop unrelated work. Its dependents are skipped and
// carry an explicit error explaining which prerequisite failed.
func (d *Dispatcher) RunBatch(ctx context.Context, actions []ai.Action, maxWorkers int) []ai.ActionResult {
	if len(actions) > maxActionsPerBatch {
		return d.rejectOversizedBatch(ctx, actions)
	}
	if !isBatchPlan(actions) {
		return d.Run(ctx, actions)
	}
	if maxWorkers < 1 {
		maxWorkers = 1
	}

	results := make([]ai.ActionResult, len(actions))
	state := make([]batchState, len(actions))
	byID := make(map[string]int, len(actions))
	for i, action := range actions {
		byID[action.ID] = i
	}

	remaining := len(actions)
	for remaining > 0 {
		ready := make([]int, 0, remaining)
		for i, action := range actions {
			if state[i] != batchPending {
				continue
			}
			dependencyState, dependency := dependenciesState(action.DependsOn, byID, state)
			switch dependencyState {
			case dependenciesWaiting:
				continue
			case dependenciesMissing:
				results[i] = d.finish(ctx, action, "", fmt.Errorf("skipped: dependency %q is not in this batch", dependency))
				state[i] = batchSkipped
				remaining--
			case dependenciesFailed:
				results[i] = d.finish(ctx, action, "", fmt.Errorf("skipped: dependency %q did not succeed", dependency))
				state[i] = batchSkipped
				remaining--
			case dependenciesReady:
				ready = append(ready, i)
			}
		}

		if len(ready) == 0 {
			for i, action := range actions {
				if state[i] == batchPending {
					results[i] = d.finish(ctx, action, "", fmt.Errorf("skipped: unresolved dependency cycle"))
					state[i] = batchSkipped
					remaining--
				}
			}
			continue
		}
		ready = serialReady(ready, actions)

		runBatchWorkers(ctx, d, actions, results, state, ready, maxWorkers)
		remaining -= len(ready)
	}
	return results
}

func (d *Dispatcher) rejectOversizedBatch(ctx context.Context, actions []ai.Action) []ai.ActionResult {
	results := make([]ai.ActionResult, len(actions))
	for i, action := range actions {
		results[i] = d.finish(ctx, action, "", fmt.Errorf("action batch exceeds limit of %d", maxActionsPerBatch))
	}
	return results
}

func (d *Dispatcher) runAction(ctx context.Context, action ai.Action) ai.ActionResult {
	action.Type = strings.TrimSpace(action.Type)
	reportActionProgress(ctx, ActionProgress{Action: action, State: "started"})
	handler, known := d.handlers[action.Type]
	if err := validateAction(action, known); err != nil {
		result := d.finish(ctx, action, "", err)
		reportActionProgress(ctx, ActionProgress{Action: action, State: "failed", Error: err})
		return result
	}

	policy := policyFor(action.Type)
	attempts := 1
	if policy.RetrySafe {
		attempts = 2
	}
	var message string
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
		message, err = handler(attemptCtx, action)
		if err == nil {
			if verifier := d.verifiers[action.Type]; verifier != nil {
				err = verifier(attemptCtx, action)
			}
		}
		if attemptCtx.Err() != nil && err == nil {
			err = attemptCtx.Err()
		}
		cancel()
		if err == nil || attempt == attempts || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			result := d.finish(ctx, action, message, err)
			reportActionProgress(ctx, ActionProgress{Action: action, State: "failed", Message: message, Error: err})
			return result
		case <-time.After(100 * time.Millisecond):
		}
	}
	result := d.finish(ctx, action, message, err)
	state := "completed"
	if err != nil {
		state = "failed"
	}
	reportActionProgress(ctx, ActionProgress{Action: action, State: state, Message: message, Error: err})
	return result
}

// serialReady prevents two file/database writes from racing merely because a
// model declared them independent. Read-only actions can still share a batch.
func serialReady(ready []int, actions []ai.Action) []int {
	for _, index := range ready {
		if !policyFor(actions[index].Type).ParallelSafe {
			return []int{index}
		}
	}
	return ready
}

func (d *Dispatcher) finish(ctx context.Context, action ai.Action, message string, err error) ai.ActionResult {
	result := newActionResult(action, message, err)
	if d.auditor == nil {
		return result
	}

	payload, marshalErr := json.Marshal(action)
	if marshalErr != nil {
		return newActionResult(action, message, fmt.Errorf("encode action audit: %w", marshalErr))
	}
	entry := models.ActionAudit{
		ActionType: action.Type,
		ActionJSON: string(payload),
		Outcome:    "succeeded",
		Message:    message,
		CreatedAt:  time.Now(),
	}
	if err != nil {
		entry.Outcome = "failed"
		entry.Error = err.Error()
	}
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	auditErr := d.auditor.Record(auditCtx, entry)
	cancel()
	if auditErr == nil {
		return result
	}
	if result.Err != nil {
		result.Error += "; audit record failed: " + auditErr.Error()
		return result
	}
	result.Message += " (warning: action audit was not recorded)"
	return result
}

type batchState uint8

const (
	batchPending batchState = iota
	batchSucceeded
	batchFailed
	batchSkipped
)

type dependencyStatus uint8

const (
	dependenciesReady dependencyStatus = iota
	dependenciesWaiting
	dependenciesMissing
	dependenciesFailed
)

func isBatchPlan(actions []ai.Action) bool {
	if len(actions) < 2 {
		return false
	}
	seen := make(map[string]bool, len(actions))
	for _, action := range actions {
		if action.ID == "" || seen[action.ID] {
			return false
		}
		seen[action.ID] = true
	}
	return true
}

func dependenciesState(dependsOn []string, byID map[string]int, state []batchState) (dependencyStatus, string) {
	for _, id := range dependsOn {
		i, ok := byID[id]
		if !ok {
			return dependenciesMissing, id
		}
		switch state[i] {
		case batchPending:
			return dependenciesWaiting, id
		case batchFailed, batchSkipped:
			return dependenciesFailed, id
		}
	}
	return dependenciesReady, ""
}

func runBatchWorkers(ctx context.Context, d *Dispatcher, actions []ai.Action, results []ai.ActionResult, state []batchState, ready []int, maxWorkers int) {
	workers := maxWorkers
	if workers > len(ready) {
		workers = len(ready)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = d.runAction(ctx, actions[i])
				if results[i].Err != nil {
					state[i] = batchFailed
				} else {
					state[i] = batchSucceeded
				}
			}
		}()
	}
	for _, i := range ready {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

func newActionResult(action ai.Action, message string, err error) ai.ActionResult {
	result := ai.ActionResult{Action: action, Message: message, Err: err}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}
