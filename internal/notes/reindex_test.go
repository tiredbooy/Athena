package notes

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/storage"
)

// reindexEmbedder stands in for a provider whose configured embedding model can
// change under the index — the exact situation V-03 has to make visible.
type reindexEmbedder struct {
	model string
	width int
	fail  error
}

func (e *reindexEmbedder) Name() string       { return "reindex-test" }
func (e *reindexEmbedder) EmbedModel() string { return e.model }

func (e *reindexEmbedder) Embed(context.Context, string) ([]float32, error) {
	if e.fail != nil {
		return nil, e.fail
	}
	return make([]float32, e.width), nil
}

func newReindexService(t *testing.T, embedder *reindexEmbedder) *Service {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(t.TempDir(), storage.NewNoteStore(db), storage.NewChunkStore(db), embedder).
		TrackJobsIn(storage.NewJobStore(db))
}

// V-03: switching embedding models silently poisons search — the old vectors
// stay in the index and the new query vector is compared against them. Athena
// has to record what built the index and notice when that stops matching.
func TestReindexRecordsItsModelAndIndexHealthCatchesAChange(t *testing.T) {
	embedder := &reindexEmbedder{model: "old-embed", width: 4}
	service := newReindexService(t, embedder)

	if _, created, err := service.CreateNote(t.Context(), "Vector Source", "body", "", nil); err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}

	// Nothing has been rebuilt yet, so what produced the vectors is unknown —
	// and unknown must not be reported as a mismatch.
	health, err := service.IndexHealth()
	if err != nil {
		t.Fatalf("index health before any reindex: %v", err)
	}
	if health.Mismatch || health.IndexedWith != "" || health.LastStatus != "" {
		t.Fatalf("health before any reindex = %+v, want unknown and no warning", health)
	}

	if err := service.Reindex(t.Context(), nil); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	health, err = service.IndexHealth()
	if err != nil {
		t.Fatalf("index health after reindex: %v", err)
	}
	if health.IndexedWith != "old-embed" || health.Dimensions != 4 {
		t.Fatalf("health = %+v, want the model and width that built the index", health)
	}
	if health.Mismatch {
		t.Fatal("a fresh index must not report a mismatch against its own model")
	}
	if health.LastStatus != storage.JobDone || health.LastRun.IsZero() {
		t.Fatalf("last run = %q at %v, want a finished job", health.LastStatus, health.LastRun)
	}

	// The user switches embedding models. The vectors on disk are still the old
	// ones, so search is now comparing across two embedding spaces.
	embedder.model = "new-embed"
	health, err = service.IndexHealth()
	if err != nil {
		t.Fatalf("index health after model switch: %v", err)
	}
	if !health.Mismatch || health.IndexedWith != "old-embed" || health.ConfiguredAs != "new-embed" {
		t.Fatalf("health after switching models = %+v, want a reported mismatch", health)
	}
}

// V-03: a reindex that dies partway leaves the old index in place, so the
// record must keep pointing at the model that really built it. Reporting the
// half-attempted model would tell the user their index is fine when the failed
// switch is exactly what they need to retry.
func TestFailedReindexIsRecordedWithoutClaimingTheIndex(t *testing.T) {
	embedder := &reindexEmbedder{model: "old-embed", width: 4}
	service := newReindexService(t, embedder)

	if _, created, err := service.CreateNote(t.Context(), "Vector Source", "body", "", nil); err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	if err := service.Reindex(t.Context(), nil); err != nil {
		t.Fatalf("first reindex: %v", err)
	}

	embedder.model, embedder.fail = "new-embed", errors.New("provider unreachable")
	if err := service.Reindex(t.Context(), nil); err == nil {
		t.Fatal("expected the embedding failure to be reported")
	}

	health, err := service.IndexHealth()
	if err != nil {
		t.Fatalf("index health after a failed reindex: %v", err)
	}
	if health.IndexedWith != "old-embed" {
		t.Fatalf("index attributed to %q; a failed run never replaced the vectors", health.IndexedWith)
	}
	if health.LastStatus != storage.JobFailed || health.LastError == "" {
		t.Fatalf("last run = %q / %q, want a recorded failure the user can see", health.LastStatus, health.LastError)
	}
	if !health.Mismatch {
		t.Fatal("the configured model still does not match the index; that has to stay visible")
	}
}
