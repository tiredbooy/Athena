package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/chat"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
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

	fmt.Println("second-brain ready.")
	fmt.Printf("  vault: %s\n  db:    %s\n  model: %s\n\n", cfg.VaultPath, cfg.DBPath, cfg.ChatModel)

	chat.NewLoop(client, retrievalSvc).Run()
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "failed to %s: %v\n", step, err)
	os.Exit(1)
}
