package tools

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/tiredbooy/internal/ai"
)

func TestRunBatchContinuesIndependentWorkAndSkipsDependents(t *testing.T) {
	d := NewDispatcher()
	var completed atomic.Int32
	d.Register("ok", func(_ context.Context, _ ai.Action) (string, error) {
		completed.Add(1)
		return "done", nil
	})
	d.Register("fail", func(_ context.Context, _ ai.Action) (string, error) {
		return "", errors.New("expected failure")
	})

	results := d.RunBatch(context.Background(), []ai.Action{
		{ID: "first", Type: "fail"},
		{ID: "dependent", Type: "ok", DependsOn: []string{"first"}},
		{ID: "independent", Type: "ok"},
	}, 2)

	if results[0].Err == nil || results[0].Error != "expected failure" {
		t.Fatalf("failing result = %+v, want transport-safe error", results[0])
	}
	if results[1].Err == nil || results[1].Message != "" {
		t.Fatalf("dependent result = %+v, want skipped error", results[1])
	}
	if results[2].Err != nil || results[2].Message != "done" {
		t.Fatalf("independent result = %+v, want success", results[2])
	}
	if completed.Load() != 1 {
		t.Fatalf("completed handlers = %d, want 1", completed.Load())
	}
}

func TestRunBatchFallsBackToSequentialActionsWithoutIDs(t *testing.T) {
	d := NewDispatcher()
	var calls []string
	d.Register("record", func(_ context.Context, action ai.Action) (string, error) {
		calls = append(calls, action.Title)
		return action.Title, nil
	})

	results := d.RunBatch(context.Background(), []ai.Action{
		{Type: "record", Title: "first"},
		{Type: "record", Title: "second"},
	}, 4)

	if len(results) != 2 || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("calls = %v, want sequential order", calls)
	}
}
