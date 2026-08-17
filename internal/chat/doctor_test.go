package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/notes"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
)

// doctorVault returns a writable vault directory and a retrieval service backed
// by a throwaway SQLite file, so /doctor's index and vault checks both run
// against real dependencies without touching the user's home directory.
func doctorVault(t *testing.T) (string, *retrieval.Service) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return vault, retrieval.NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), taskStateEmbedder{})
}

// namedEmbedder is an embedding provider identified only by its model name, so
// a test can rebuild the index under one model and configure another.
type namedEmbedder struct{ model string }

func (e namedEmbedder) Name() string       { return e.model }
func (e namedEmbedder) EmbedModel() string { return e.model }
func (e namedEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}

// doctorIndexVault returns a vault, a retrieval service, and a constructor for
// notes services sharing one database. The constructor takes the embedding
// model because the failure /doctor has to catch is exactly a disagreement
// between the model that built the vectors and the one configured now, and the
// jobs table is the only place that disagreement is recorded.
func doctorIndexVault(t *testing.T) (string, *retrieval.Service, func(embedModel string) *notes.Service) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	vault := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	noteStore, chunkStore, jobs := storage.NewNoteStore(db), storage.NewChunkStore(db), storage.NewJobStore(db)
	newNotes := func(embedModel string) *notes.Service {
		return notes.NewService(vault, noteStore, chunkStore, namedEmbedder{model: embedModel}).TrackJobsIn(jobs)
	}
	return vault, retrieval.NewService(vault, noteStore, chunkStore, namedEmbedder{model: "nomic-embed-text"}), newNotes
}

// An index built by one embedding model and searched with another returns
// plausible nonsense instead of an error. Nothing else in Athena can notice it,
// so /doctor must report it as a problem the user has to fix.
func TestDoctorFlagsEmbeddingModelMismatch(t *testing.T) {
	vault, retrievalSvc, newNotes := doctorIndexVault(t)
	if err := newNotes("stale-embed-model").Reindex(context.Background(), nil); err != nil {
		t.Fatalf("build index with the stale model: %v", err)
	}

	loop := &Loop{
		retrieval: retrievalSvc,
		providers: map[string]ai.ChatProvider{},
		config:    &config.Config{VaultPath: vault},
		notes:     newNotes("nomic-embed-text"),
	}
	report := loop.Doctor(context.Background())

	line := doctorLine(t, report, "Embedding index")
	if !strings.HasPrefix(line, "!") || !strings.Contains(line, `"stale-embed-model"`) {
		t.Fatalf("index line = %q, want a ! line naming the model that built the vectors", line)
	}
	if !strings.Contains(line, "/reindex") {
		t.Fatalf("index line = %q, want the command that repairs it", line)
	}
	if !strings.Contains(report, "1 of 3 checks need attention") {
		t.Fatalf("summary did not count the mismatch:\n%s", report)
	}
}

// A vault that was never rebuilt has no recorded model. That is unknown, not
// broken: reporting it as a problem would teach the user to ignore the line
// above, which is the one that means search is actually returning nonsense.
func TestDoctorDoesNotFlagAnIndexThatWasNeverRebuilt(t *testing.T) {
	vault, retrievalSvc, newNotes := doctorIndexVault(t)
	loop := &Loop{
		retrieval: retrievalSvc,
		providers: map[string]ai.ChatProvider{},
		config:    &config.Config{VaultPath: vault},
		notes:     newNotes("nomic-embed-text"),
	}

	line := doctorLine(t, loop.Doctor(context.Background()), "Embedding index")
	if !strings.HasPrefix(line, "✓") || !strings.Contains(line, "no rebuild recorded") {
		t.Fatalf("index line = %q, want a passing line that says the model is unknown", line)
	}
}

// /reindex is the user's way out of the mismatch above. It is a command and not
// an action type on purpose: re-embedding a whole vault is expensive, so a weak
// local model must not be able to propose it.
func TestReindexCommandRebuildsTheIndex(t *testing.T) {
	ctx := context.Background()
	vault, retrievalSvc, newNotes := doctorIndexVault(t)
	if err := newNotes("stale-embed-model").Reindex(ctx, nil); err != nil {
		t.Fatalf("build index with the stale model: %v", err)
	}
	current := newNotes("nomic-embed-text")
	for _, title := range []string{"First", "Second"} {
		if _, _, err := current.CreateNote(ctx, title, "body", "", nil); err != nil {
			t.Fatalf("create note %q: %v", title, err)
		}
	}

	loop := &Loop{
		ai:        &pickerProvider{name: "Ollama", model: "qwen3:1.7b"},
		retrieval: retrievalSvc,
		providers: map[string]ai.ChatProvider{},
		config:    &config.Config{VaultPath: vault},
		notes:     current,
	}
	var statuses []string
	reply, err := NewSession(loop).Submit(ctx, "/reindex", func(status string) { statuses = append(statuses, status) }, nil)
	if err != nil {
		t.Fatalf("/reindex: %v", err)
	}
	if !strings.Contains(reply, "2 note(s)") {
		t.Fatalf("reply = %q, want the number of notes it re-embedded", reply)
	}
	// Progress goes through the ordinary status callback, so a UI does not need
	// a second channel to show a job that runs for minutes.
	if !strings.Contains(strings.Join(statuses, "\n"), "note 2 of 2") {
		t.Fatalf("statuses = %q, want per-note progress", statuses)
	}
	line := doctorLine(t, loop.Doctor(ctx), "Embedding index")
	if !strings.HasPrefix(line, "✓") || !strings.Contains(line, "nomic-embed-text") {
		t.Fatalf("index line = %q, want a passing line naming the model that rebuilt it", line)
	}
}

// doctorLine returns the single report line for a named check. /doctor's whole
// value is that a user can read one line per dependency, so tests assert on the
// line, not on a substring that could match anywhere in the report.
func doctorLine(t *testing.T, report, name string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, " "+name+" — ") {
			return line
		}
	}
	t.Fatalf("no %q line in report:\n%s", name, report)
	return ""
}

// doctorOllama serves /api/tags with exactly the named models. It stands in for
// a local Ollama so the embedding check is exercised without one running.
func doctorOllama(t *testing.T, names ...string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		var b strings.Builder
		b.WriteString(`{"models":[`)
		for i, name := range names {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"name":"` + name + `"}`)
		}
		b.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// A vault Athena cannot write to is the failure that silently loses a user's
// notes, so /doctor has to name it before anything is written.
func TestDoctorReportsUnwritableVault(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write permission bit, so an unwritable vault cannot be simulated")
	}
	vault, retrievalSvc := doctorVault(t)
	if err := os.Chmod(vault, 0o500); err != nil {
		t.Fatalf("make vault read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(vault, 0o700) })

	loop := &Loop{retrieval: retrievalSvc, providers: map[string]ai.ChatProvider{}, config: &config.Config{VaultPath: vault}}
	report := loop.Doctor(context.Background())

	line := doctorLine(t, report, "Vault")
	if !strings.HasPrefix(line, "!") || !strings.Contains(line, "cannot write") {
		t.Fatalf("vault line = %q, want a ! line naming the write failure", line)
	}
	if !strings.Contains(report, "need attention") {
		t.Fatalf("summary did not report a problem:\n%s", report)
	}
}

func TestDoctorReportsMissingVaultConfiguration(t *testing.T) {
	_, retrievalSvc := doctorVault(t)
	loop := &Loop{retrieval: retrievalSvc, providers: map[string]ai.ChatProvider{}}

	line := doctorLine(t, loop.Doctor(context.Background()), "Vault")
	if !strings.HasPrefix(line, "!") || !strings.Contains(line, "configuration is unavailable") {
		t.Fatalf("vault line = %q, want a ! line for missing configuration", line)
	}
}

// Retrieval degrades to nothing without embeddings, so a missing embed model is
// reported with the exact pull command instead of surfacing later as empty
// search results.
func TestDoctorReportsMissingEmbedModel(t *testing.T) {
	vault, retrievalSvc := doctorVault(t)
	host := doctorOllama(t, "qwen3:1.7b")
	client := ai.NewClient(host, "qwen3:1.7b", "nomic-embed-text")

	loop := &Loop{
		ai:        client,
		providers: map[string]ai.ChatProvider{"ollama": client},
		retrieval: retrievalSvc,
		config:    &config.Config{VaultPath: vault},
	}
	report := loop.Doctor(context.Background())

	line := doctorLine(t, report, "Local embeddings")
	if !strings.HasPrefix(line, "!") || !strings.Contains(line, `"nomic-embed-text" is not pulled`) {
		t.Fatalf("embeddings line = %q, want a ! line naming the missing model", line)
	}
	if !strings.Contains(line, "ollama pull nomic-embed-text") {
		t.Fatalf("embeddings line = %q, want the pull command the user should run", line)
	}
	// The chat model is present, so only the embedding check may fail: a broken
	// embed model must not be reported as a broken provider.
	if chat := doctorLine(t, report, "Ollama"); !strings.HasPrefix(chat, "✓") {
		t.Fatalf("ollama chat line = %q, want it to pass", chat)
	}
}

func TestDoctorReportsProviderProblems(t *testing.T) {
	vault, retrievalSvc := doctorVault(t)
	loop := &Loop{
		providers: map[string]ai.ChatProvider{
			"unreachable": &pickerProvider{name: "Unreachable", model: "grok-4", catalogErr: errors.New("get models: connection refused")},
			"stale":       &pickerProvider{name: "Stale", model: "grok-4", catalog: []string{"grok-3"}},
		},
		retrieval: retrievalSvc,
		config:    &config.Config{VaultPath: vault},
	}
	report := loop.Doctor(context.Background())

	// An unreachable provider gets the actionable hint, not just the raw error.
	line := doctorLine(t, report, "Unreachable")
	if !strings.HasPrefix(line, "!") || !strings.Contains(line, "check the endpoint or use /connect") {
		t.Fatalf("unreachable provider line = %q, want a ! line with reconnect guidance", line)
	}
	// A selected model the provider no longer offers fails every turn, so the
	// catalog being readable is not enough for this check to pass.
	line = doctorLine(t, report, "Stale")
	if !strings.HasPrefix(line, "!") || !strings.Contains(line, `selected model "grok-4" was not listed`) {
		t.Fatalf("stale provider line = %q, want a ! line naming the missing model", line)
	}
	if !strings.Contains(report, "2 of 4 checks need attention") {
		t.Fatalf("summary did not count both provider problems:\n%s", report)
	}
}

func TestDoctorReportsUnreadableVaultIndex(t *testing.T) {
	vault, _ := doctorVault(t)
	db, err := storage.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	closed := retrieval.NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), taskStateEmbedder{})
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	loop := &Loop{retrieval: closed, providers: map[string]ai.ChatProvider{}, config: &config.Config{VaultPath: vault}}
	line := doctorLine(t, loop.Doctor(context.Background()), "SQLite vault index")
	if !strings.HasPrefix(line, "!") {
		t.Fatalf("index line = %q, want a ! line when the index cannot be read", line)
	}
}

func TestDoctorPassesWhenEveryDependencyIsHealthy(t *testing.T) {
	vault, retrievalSvc := doctorVault(t)
	host := doctorOllama(t, "qwen3:1.7b", "nomic-embed-text")
	client := ai.NewClient(host, "qwen3:1.7b", "nomic-embed-text")

	loop := &Loop{
		ai:        client,
		providers: map[string]ai.ChatProvider{"ollama": client},
		retrieval: retrievalSvc,
		config:    &config.Config{VaultPath: vault},
	}
	report := loop.Doctor(context.Background())

	if strings.Contains(report, "\n!") {
		t.Fatalf("healthy setup reported a problem:\n%s", report)
	}
	if !strings.Contains(report, "All 4 checks passed.") {
		t.Fatalf("summary = %q, want all 4 checks passing", report)
	}
}
