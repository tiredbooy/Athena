package tools

import (
	"context"
	"fmt"
	"sync"

	"github.com/tiredbooy/internal/ai"
)

type Handler func(ctx context.Context, a ai.Action) (string, error)

type Dispatcher struct {
	handlers map[string]Handler
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]Handler)}
}

func (d *Dispatcher) Register(actionType string, h Handler) {
	d.handlers[actionType] = h
}

func (d *Dispatcher) Run(ctx context.Context, actions []ai.Action) []ai.ActionResult {
	results := make([]ai.ActionResult, 0, len(actions))
	for _, a := range actions {
		h, ok := d.handlers[a.Type]
		if !ok {
			results = append(results, newActionResult(a, "", fmt.Errorf("unknown action type %q", a.Type)))
			continue
		}
		msg, err := h(ctx, a)
		results = append(results, newActionResult(a, msg, err))
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
				results[i] = newActionResult(action, "", fmt.Errorf("skipped: dependency %q is not in this batch", dependency))
				state[i] = batchSkipped
				remaining--
			case dependenciesFailed:
				results[i] = newActionResult(action, "", fmt.Errorf("skipped: dependency %q did not succeed", dependency))
				state[i] = batchSkipped
				remaining--
			case dependenciesReady:
				ready = append(ready, i)
			}
		}

		if len(ready) == 0 {
			for i, action := range actions {
				if state[i] == batchPending {
					results[i] = newActionResult(action, "", fmt.Errorf("skipped: unresolved dependency cycle"))
					state[i] = batchSkipped
					remaining--
				}
			}
			continue
		}

		runBatchWorkers(ctx, d, actions, results, state, ready, maxWorkers)
		remaining -= len(ready)
	}
	return results
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
				action := actions[i]
				handler, ok := d.handlers[action.Type]
				if !ok {
					results[i] = newActionResult(action, "", fmt.Errorf("unknown action type %q", action.Type))
					state[i] = batchFailed
					continue
				}
				message, err := handler(ctx, action)
				results[i] = newActionResult(action, message, err)
				if err != nil {
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
