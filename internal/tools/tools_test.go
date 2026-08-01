package tools

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
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

func TestRunRejectsInvalidActionBeforeHandler(t *testing.T) {
	d := NewDispatcher()
	called := false
	d.Register("create_note", func(_ context.Context, _ ai.Action) (string, error) {
		called = true
		return "created", nil
	})

	result := d.Run(context.Background(), []ai.Action{{Type: "create_note"}})[0]
	if result.Err == nil || result.Error != "create_note requires title" {
		t.Fatalf("result = %+v, want title validation error", result)
	}
	if called {
		t.Fatal("handler ran despite failed preflight validation")
	}
}

func TestRunRetriesReadOnlyActionAndRecordsOutcome(t *testing.T) {
	d := NewDispatcher()
	audit := &recordingAudit{}
	d.SetAuditLogger(audit)
	var attempts atomic.Int32
	d.Register("folder_exists", func(_ context.Context, _ ai.Action) (string, error) {
		if attempts.Add(1) == 1 {
			return "", errors.New("temporary read failure")
		}
		return "folder exists", nil
	})

	result := d.Run(context.Background(), []ai.Action{{Type: "folder_exists", Folder: "projects"}})[0]
	if result.Err != nil || result.Message != "folder exists" {
		t.Fatalf("result = %+v, want successful retry", result)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	if len(audit.entries) != 1 || audit.entries[0].Outcome != "succeeded" {
		t.Fatalf("audit entries = %+v, want one successful record", audit.entries)
	}
}

func TestRunFailsWhenWriteCannotBeVerified(t *testing.T) {
	d := NewDispatcher()
	d.Register("create_note", func(_ context.Context, _ ai.Action) (string, error) {
		return "created", nil
	})
	d.RegisterVerifier("create_note", func(_ context.Context, _ ai.Action) error {
		return errors.New("record not found after write")
	})

	result := d.Run(context.Background(), []ai.Action{{Type: "create_note", Title: "Plan"}})[0]
	if result.Err == nil || result.Error != "record not found after write" {
		t.Fatalf("result = %+v, want verification failure", result)
	}
}

type recordingAudit struct {
	entries []models.ActionAudit
}

func (a *recordingAudit) Record(_ context.Context, entry models.ActionAudit) error {
	a.entries = append(a.entries, entry)
	return nil
}
