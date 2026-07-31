package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/chat"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/notes"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/tools"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		fatal("load config", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		fatal("prepare directories", err)
	}

	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		fatal("open database", err)
	}
	defer db.Close()

	noteStore := storage.NewNoteStore(db)
	chunkStore := storage.NewChunkStore(db)

	client := ai.NewClient(cfg.OllamaHost, cfg.ChatModel, cfg.EmbedModel)
	if err := client.EnsureRunning(ctx); err != nil {
		fatal("start ollama", err)
	}

	retrievalSvc := retrieval.NewService(noteStore, chunkStore, client)
	notesSvc := notes.NewService(cfg.VaultPath, noteStore, chunkStore, client)

	dispatcher := buildDispatcher(notesSvc)

	fmt.Println("second-brain ready.")
	fmt.Printf("  vault: %s\n  db:    %s\n  model: %s\n\n", cfg.VaultPath, cfg.DBPath, cfg.ChatModel)

	chat.NewLoop(client, retrievalSvc, dispatcher).Run()
}

// buildDispatcher registers every action type the model is allowed to
// invoke (see ai/prompt.go for the schema advertised to the model).
// Add a handler here whenever a new action type is added to that prompt.
func buildDispatcher(notesSvc *notes.Service) *tools.Dispatcher {
	d := tools.NewDispatcher()

	d.Register("create_note", func(ctx context.Context, a ai.Action) (string, error) {
		n, err := notesSvc.CreateNote(ctx, a.Title, a.Content, a.Tags)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Created note %q at %s", n.Title, n.Path), nil
	})

	d.Register("create_task", func(ctx context.Context, a ai.Action) (string, error) {
		n, err := notesSvc.CreateTask(ctx, a.Title, a.Content)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Created task %q at %s", n.Title, n.Path), nil
	})

	d.Register("update_note", func(ctx context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("update_note requires note_id")
		}
		if err := notesSvc.UpdateNote(ctx, a.NoteID, a.Content); err != nil {
			return "", err
		}
		return fmt.Sprintf("Updated note %d", a.NoteID), nil
	})

	d.Register("mark_done", func(ctx context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("mark_done requires note_id")
		}
		if err := notesSvc.MarkDone(a.NoteID, a.Done); err != nil {
			return "", err
		}
		state := "done"
		if !a.Done {
			state = "not done"
		}
		return fmt.Sprintf("Marked task %d as %s", a.NoteID, state), nil
	})

	return d
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "failed to %s: %v\n", step, err)
	os.Exit(1)
}
