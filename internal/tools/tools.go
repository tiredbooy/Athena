package tools

import (
	"context"
	"fmt"

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
			results = append(results, ai.ActionResult{
				Action: a,
				Err:    fmt.Errorf("unknown action type %q", a.Type),
			})
			continue
		}
		msg, err := h(ctx, a)
		results = append(results, ai.ActionResult{Action: a, Message: msg, Err: err})
	}
	return results
}
