package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
)

// Doctor is application-level diagnostics: it checks real dependencies but
// never sends a model prompt or changes vault data.
func (l *Loop) Doctor(ctx context.Context) string {
	var out strings.Builder
	out.WriteString("Athena diagnostics\n")
	checks, problems := 0, 0
	check := func(ok bool, name, detail string) {
		checks++
		if ok {
			fmt.Fprintf(&out, "✓ %s — %s\n", name, detail)
			return
		}
		problems++
		fmt.Fprintf(&out, "! %s — %s\n", name, detail)
	}

	if err := l.retrievalCheck(); err != nil {
		check(false, "SQLite vault index", err.Error())
	} else {
		check(true, "SQLite vault index", "readable")
	}
	// V-03: an index built by one embedding model and searched with another
	// returns plausible nonsense rather than an error — nothing else in Athena
	// can notice it, which is why this is a problem line and not a note. Skipped
	// entirely when no vault service is wired in, since there is then nothing
	// that could rebuild the index anyway.
	if l.notes != nil {
		health, err := l.notes.IndexHealth()
		switch {
		case err != nil:
			check(false, "Embedding index", err.Error())
		case health.Mismatch:
			check(false, "Embedding index", fmt.Sprintf("vectors were built with %q but %q is configured; search results are meaningless until you run /reindex", health.IndexedWith, health.ConfiguredAs))
		case health.IndexedWith == "":
			// Unknown is not a mismatch (see notes.IndexHealth): warning about
			// every vault that has never been rebuilt would teach the user to
			// ignore the line above, which is the one that matters.
			check(true, "Embedding index", fmt.Sprintf("%s is configured; no rebuild recorded, so what built the vectors is unknown — /reindex makes it certain", health.ConfiguredAs))
		default:
			check(true, "Embedding index", fmt.Sprintf("built with %s at %d dimensions, matching the configured model", health.IndexedWith, health.Dimensions))
		}
	}
	if l.config == nil {
		check(false, "Vault", "configuration is unavailable")
	} else if err := checkWritableDir(l.config.VaultPath); err != nil {
		check(false, "Vault", err.Error())
	} else {
		check(true, "Vault", "readable and writable")
	}

	ids := make([]string, 0, len(l.providers))
	for id := range l.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		provider := l.providers[id]
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		models, err := provider.ChatModels(probeCtx)
		cancel()
		if err != nil {
			check(false, provider.Name(), diagnoseProviderError(err))
			continue
		}
		current := provider.ChatModel()
		found := false
		for _, model := range models {
			if model.Name == current {
				found = true
				break
			}
		}
		detail := fmt.Sprintf("model catalog available; %d model(s); tool adapter available", len(models))
		if !found {
			detail += fmt.Sprintf("; selected model %q was not listed", current)
		}
		check(found, provider.Name(), detail)
	}
	if local, ok := l.providers["ollama"].(*ai.Client); ok {
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		models, err := local.ListModels(probeCtx)
		cancel()
		if err != nil {
			check(false, "Local embeddings", diagnoseProviderError(err))
		} else {
			found := false
			for _, model := range models {
				if model.Name == local.EmbedModel() {
					found = true
					break
				}
			}
			if found {
				check(true, "Local embeddings", local.EmbedModel()+" is available")
			} else {
				check(false, "Local embeddings", fmt.Sprintf("%q is not pulled; run: ollama pull %s", local.EmbedModel(), local.EmbedModel()))
			}
		}
	}
	if problems == 0 {
		fmt.Fprintf(&out, "All %d checks passed.", checks)
	} else {
		fmt.Fprintf(&out, "%d of %d checks need attention. Fix the lines marked !, then run /doctor again.", problems, checks)
	}
	return strings.TrimSpace(out.String())
}

func (l *Loop) retrievalCheck() error { _, err := l.retrieval.Inventory(); return err }
func checkWritableDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("vault path is empty")
	}
	file, err := os.CreateTemp(dir, ".athena-doctor-*")
	if err != nil {
		return fmt.Errorf("cannot write %s: %w", dir, err)
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove diagnostic file: %w", err)
	}
	if _, err := filepath.Abs(dir); err != nil {
		return err
	}
	return nil
}
func diagnoseProviderError(err error) string {
	text := err.Error()
	lower := strings.ToLower(text)
	if strings.Contains(lower, "environment variable") {
		return text + "; export the named API key, then restart Athena"
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") {
		return text + "; check the endpoint or use /connect"
	}
	if strings.Contains(lower, "status 401") || strings.Contains(lower, "status 403") {
		return text + "; verify credentials with /connect"
	}
	return text
}
