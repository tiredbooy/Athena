package storage

import (
	"path/filepath"
	"testing"
)

// V-03: a reindex records which embedding model built the index, so /doctor can
// tell a poisoned index from a healthy one. That answer is only trustworthy if
// Latest can distinguish "the last attempt" from "the last attempt that
// finished" — a failed switch to a new model must not be read as the model that
// produced the vectors currently stored.
func TestLatestSeparatesLastAttemptFromLastFinishedJob(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	store := NewJobStore(db)

	finished, err := store.Create("reindex", `{"embed_model":"old-model"}`)
	if err != nil {
		t.Fatalf("create finished job: %v", err)
	}
	if err := store.Finish(finished, JobDone, `{"embed_model":"old-model","dimensions":4}`, ""); err != nil {
		t.Fatalf("finish job: %v", err)
	}

	failed, err := store.Create("reindex", `{"embed_model":"new-model"}`)
	if err != nil {
		t.Fatalf("create failed job: %v", err)
	}
	if err := store.Update(failed, JobRunning, 3, 10, "rebuilding vectors", ""); err != nil {
		t.Fatalf("record progress: %v", err)
	}
	if err := store.Finish(failed, JobFailed, `{"embed_model":"new-model"}`, "provider unreachable"); err != nil {
		t.Fatalf("fail job: %v", err)
	}

	lastAttempt, err := store.Latest("reindex", "")
	if err != nil || lastAttempt == nil {
		t.Fatalf("latest attempt: job=%v err=%v", lastAttempt, err)
	}
	if lastAttempt.ID != failed || lastAttempt.Status != JobFailed || lastAttempt.Error != "provider unreachable" {
		t.Fatalf("latest attempt = %+v, want the failed job", lastAttempt)
	}
	// Progress must survive Finish: how far a failed run got is the useful half
	// of its record.
	if lastAttempt.ProgressCurrent != 3 || lastAttempt.ProgressTotal != 10 {
		t.Fatalf("progress = %d/%d, want 3/10", lastAttempt.ProgressCurrent, lastAttempt.ProgressTotal)
	}

	lastFinished, err := store.Latest("reindex", JobDone)
	if err != nil || lastFinished == nil {
		t.Fatalf("latest finished: job=%v err=%v", lastFinished, err)
	}
	if lastFinished.ID != finished {
		t.Fatalf("latest finished = job %d, want %d", lastFinished.ID, finished)
	}
	if lastFinished.Payload != `{"embed_model":"old-model","dimensions":4}` {
		t.Fatalf("finished payload = %q; Finish must keep what the job produced", lastFinished.Payload)
	}

	// Terminal jobs must leave Active(), or a crashed run would look like it is
	// still going forever.
	active, err := store.Active()
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active jobs = %+v, want none", active)
	}
	missing, err := store.Latest("compaction", "")
	if err != nil || missing != nil {
		t.Fatalf("latest of an unused type = %v, err=%v; want nil, nil", missing, err)
	}
}
