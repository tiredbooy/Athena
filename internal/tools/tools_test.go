package tools

import (
	"context"
	"errors"
	"strings"
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

func TestRunReportsActionProgress(t *testing.T) {
	d := NewDispatcher()
	d.Register("record", func(_ context.Context, action ai.Action) (string, error) {
		return "recorded " + action.Title, nil
	})
	var progress []ActionProgress
	ctx := WithActionProgress(context.Background(), func(update ActionProgress) {
		progress = append(progress, update)
	})

	result := d.Run(ctx, []ai.Action{{Type: "record", Title: "Rumera"}})[0]
	if result.Err != nil {
		t.Fatalf("result = %+v", result)
	}
	if len(progress) != 2 || progress[0].State != "started" || progress[1].State != "completed" {
		t.Fatalf("progress = %+v, want started then completed", progress)
	}
	if progress[0].Action.Title != "Rumera" || progress[1].Message != "recorded Rumera" {
		t.Fatalf("progress payload = %+v", progress)
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

func TestRunRejectsNoteFilePathInFolderFieldBeforeHandler(t *testing.T) {
	d := NewDispatcher()
	called := false
	d.Register("move_note", func(_ context.Context, _ ai.Action) (string, error) {
		called = true
		return "moved", nil
	})

	result := d.Run(context.Background(), []ai.Action{{
		Type:   "move_note",
		NoteID: 7,
		Folder: "books/reading/designing-data-intensive-applications.md",
	}})[0]
	want := `move_note folder must be a folder path, not a note file path "books/reading/designing-data-intensive-applications.md"`
	if result.Err == nil || result.Error != want {
		t.Fatalf("result = %+v, want %q", result, want)
	}
	if called {
		t.Fatal("handler ran despite invalid folder semantics")
	}
}

func TestRunRejectsUnsafeGraphNodeSize(t *testing.T) {
	d := NewDispatcher()
	d.Register("set_graph_node_size", func(_ context.Context, _ ai.Action) (string, error) {
		t.Fatal("handler ran despite invalid node size")
		return "", nil
	})

	result := d.Run(context.Background(), []ai.Action{{Type: "set_graph_node_size", NodeSizeMultiplier: 4}})[0]
	if result.Err == nil || result.Error != "set_graph_node_size requires node_size_multiplier between 0.25 and 3" {
		t.Fatalf("result = %+v", result)
	}
}

func TestValidateChecksPlansWithoutRunningHandlers(t *testing.T) {
	d := NewDispatcher()
	d.Register("create_note", func(_ context.Context, _ ai.Action) (string, error) {
		t.Fatal("validation must not run handlers")
		return "", nil
	})

	if err := d.Validate([]ai.Action{{Type: "create_note", Title: "Plan"}}); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	if err := d.Validate([]ai.Action{{Type: "create_note", Folder: "notes.md", Title: "Plan"}}); err == nil {
		t.Fatal("invalid note-file folder was accepted")
	}
}

func TestRejectedPlanIsRecordedForDiagnostics(t *testing.T) {
	d := NewDispatcher()
	audit := &recordingAudit{}
	d.SetAuditLogger(audit)
	d.Register("create_folder", func(context.Context, ai.Action) (string, error) { return "unused", nil })
	actions := []ai.Action{{Type: "create_folder"}}
	err := d.Validate(actions)
	if err == nil {
		t.Fatal("invalid folder plan was accepted")
	}
	d.RecordRejectedPlan(actions, err)
	if len(audit.entries) != 1 || audit.entries[0].Outcome != "rejected" || !strings.Contains(audit.entries[0].ActionJSON, `"type":"create_folder"`) || !strings.Contains(audit.entries[0].Error, "requires folder") {
		t.Fatalf("audit entries = %+v", audit.entries)
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

func TestRunRejectsUnsafeSectionReplacementWithoutGuard(t *testing.T) {
	d := NewDispatcher()
	called := false
	d.Register("replace_section", func(_ context.Context, _ ai.Action) (string, error) {
		called = true
		return "updated", nil
	})

	result := d.Run(context.Background(), []ai.Action{{Type: "replace_section", NoteID: 1, Section: "Summary", Content: "New"}})[0]
	if result.Err == nil || result.Error != "replace_section requires section and expected_content" {
		t.Fatalf("result = %+v", result)
	}
	if called {
		t.Fatal("section replacement handler ran without its stale-write guard")
	}
}

func TestPolicyDeclaresReadRetryAndDestructiveConfirmation(t *testing.T) {
	read, ok := PolicyFor("folder_exists")
	if !ok || read.Kind != ToolRead || !read.RetrySafe || !read.ParallelSafe {
		t.Fatalf("read policy = %+v", read)
	}
	destructive, ok := PolicyFor("trash_note")
	if !ok || destructive.Kind != ToolDestructive || !destructive.RequiresConfirmation || destructive.RetrySafe {
		t.Fatalf("destructive policy = %+v", destructive)
	}
	if !RequiresConfirmation([]ai.Action{{Type: "append_note"}, {Type: "create_note"}}) {
		t.Fatal("write batch should require confirmation")
	}
	if RequiresConfirmation([]ai.Action{{Type: "list_folders"}, {Type: "folder_exists"}}) {
		t.Fatal("read-only batch should not require confirmation")
	}
	if !RequiresConfirmation([]ai.Action{{Type: "ensure_folders", Paths: []string{"projects"}}}) {
		t.Fatal("folder creation should require confirmation")
	}
}

func TestSerialReadyKeepsWritesOutOfParallelBatch(t *testing.T) {
	ready := serialReady([]int{0, 1}, []ai.Action{{Type: "list_folders"}, {Type: "create_note"}})
	if len(ready) != 1 || ready[0] != 1 {
		t.Fatalf("ready = %v, want only write action", ready)
	}
}

type recordingAudit struct {
	entries []models.ActionAudit
}

func (a *recordingAudit) Record(_ context.Context, entry models.ActionAudit) error {
	a.entries = append(a.entries, entry)
	return nil
}
