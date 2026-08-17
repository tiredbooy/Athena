package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/books"
	"github.com/tiredbooy/internal/chat"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/notes"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/tools"
	"github.com/tiredbooy/internal/transport/stdio"
	"github.com/tiredbooy/internal/tui"
	"github.com/tiredbooy/internal/utils"
)

func main() {
	engineMode := len(os.Args) > 1 && os.Args[1] == "engine"
	legacyMode := len(os.Args) > 1 && os.Args[1] == "--legacy-tui"
	forceTypeScript := len(os.Args) > 1 && os.Args[1] == "--tui"
	if !engineMode && !legacyMode {
		started, err := launchTypeScriptTUI()
		if started {
			if err != nil {
				fatal("run TypeScript TUI", err)
			}
			return
		}
		if forceTypeScript {
			if err != nil {
				fatal("start TypeScript TUI", err)
			}
			fatal("start TypeScript TUI", fmt.Errorf("built TUI entrypoint not found; run npm install && npm run build in apps/tui"))
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: TypeScript TUI unavailable: %v; using legacy Go TUI\n", err)
		}
	}
	ctx, stop := processContext()
	defer stop()

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
	credentialStore, err := config.LoadCredentialStore()
	if err != nil {
		fatal("load provider credentials", err)
	}

	client := ai.NewClient(cfg.OllamaHost, cfg.ChatModel, cfg.EmbedModel)
	usesLocalEmbeddings := cfg.EmbeddingProvider.Type != "openai_compatible"
	if usesLocalEmbeddings || cfg.ActiveProvider == "" || cfg.ActiveProvider == "ollama" {
		if err := client.EnsureRunning(ctx); err != nil {
			fatal("start ollama", err)
		}
	}

	embeddings := ai.EmbeddingProvider(client)
	if cfg.EmbeddingProvider.Type == "openai_compatible" {
		embeddings = ai.NewOpenAIEmbeddingProvider(cfg.EmbeddingProvider.Name, cfg.EmbeddingProvider.BaseURL, cfg.EmbeddingProvider.APIKeyEnv, cfg.EmbeddingProvider.Model)
	}
	retrievalSvc := retrieval.NewService(cfg.VaultPath, noteStore, chunkStore, embeddings)
	// TrackJobsIn is what makes /reindex auditable: the job row is the only
	// durable record of which embedding model built the vectors, and /doctor's
	// index-health line reads it.
	notesSvc := notes.NewService(cfg.VaultPath, noteStore, chunkStore, embeddings).TrackJobsIn(storage.NewJobStore(db))
	if err := notesSvc.SyncFolderGraph(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: Obsidian folder graph is unavailable: %v\n", err)
	}
	// V-07: the vault is a folder the user also edits in Obsidian, so SQLite and
	// the filesystem drift between runs. Reconcile indexes what it can and
	// reports the rest; a vault it cannot reconcile is a warning, never a reason
	// to refuse to start — the user needs Athena running to fix it. A partial
	// scan still names what it found, so the failure and the findings are both
	// reported.
	scan, err := notesSvc.ReconcileVault(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: vault reconcile did not finish: %v\n", err)
	}
	reportVaultScan(os.Stderr, scan)

	bookResolver := books.NewResolver(storage.NewBookMetadataStore(db), nil)
	dispatcher := buildDispatcher(notesSvc, bookResolver)
	dispatcher.SetAuditLogger(storage.NewActionAuditStore(db))

	if !engineMode {
		fmt.Println("Athena ready.")
		fmt.Printf("  vault: %s\n  db:    %s\n  model: %s\n\n", cfg.VaultPath, cfg.DBPath, cfg.ChatModel)
	}

	oauth, err := ai.LoadCodexOAuth()
	if err != nil {
		fatal("load OpenAI subscription credentials", err)
	}
	xaiOAuth, err := ai.LoadXAIOAuth()
	if err != nil {
		fatal("load xAI subscription credentials", err)
	}
	// Saved providers are rebuilt through the same builder /connect uses, so a
	// provider type cannot be taught to one path and forgotten by the other. A
	// single unusable entry warns instead of aborting startup: refusing to run
	// over one bad provider would leave the user no way in to fix it.
	providers := map[string]ai.ChatProvider{"ollama": client}
	credentials := chat.ProviderCredentials{APIKeys: credentialStore, Codex: oauth, XAI: xaiOAuth}
	for _, provider := range cfg.Providers {
		connected, err := chat.BuildProvider(provider, credentials)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: provider %q is unavailable: %v\n", provider.Name, err)
			continue
		}
		providers[chat.ProviderID(provider.Name)] = connected
	}
	activeProvider := providers[cfg.ActiveProvider]
	if activeProvider == nil {
		activeProvider = client
	}
	loop := chat.NewLoop(activeProvider, providers, oauth, retrievalSvc, dispatcher, cfg)
	loop.SetNotes(notesSvc)
	loop.SetBookCatalog(bookResolver)
	loop.SetCredentialStore(credentialStore)
	loop.SetXAIOAuth(xaiOAuth)
	session := chat.NewSession(loop)
	if engineMode {
		// Serve blocks reading stdin, so a cancelled context alone cannot end it.
		// Because processContext takes SIGINT/SIGTERM over from the runtime, the
		// process no longer dies on its own: close stdin so the reader returns and
		// Serve drains the turns the cancelled context just stopped.
		go func() {
			<-ctx.Done()
			os.Stdin.Close()
		}()
		// A read error on the stdin we just closed is the shutdown, not a failure.
		if err := stdio.Serve(ctx, os.Stdin, os.Stdout, session); err != nil && ctx.Err() == nil {
			fatal("run engine", err)
		}
		return
	}
	if err := tui.RunBubble(session.Submit, session.Reset, session.HasPendingActions,
		func(ctx context.Context) ([]tui.ModelOption, error) {
			options, err := session.Models(ctx)
			out := make([]tui.ModelOption, len(options))
			for i, option := range options {
				out[i] = tui.ModelOption{ProviderID: option.ProviderID, ProviderName: option.ProviderName, Model: option.Model, Current: option.Current}
			}
			return out, err
		},
		func(ctx context.Context, option tui.ModelOption) (string, error) {
			return session.SelectModel(ctx, chat.ModelOption{ProviderID: option.ProviderID, ProviderName: option.ProviderName, Model: option.Model, Current: option.Current})
		},
		func(input tui.ConnectionInput) (string, error) {
			return session.Connect(chat.ConnectionInput{Name: input.Name, Type: input.Type, BaseURL: input.BaseURL, APIKeyEnv: input.APIKeyEnv, ChatModel: input.ChatModel})
		},
		session.StartOpenAISubscription,
	); err != nil {
		fatal("run terminal UI", err)
	}
}

// reportVaultScan prints what the startup reconcile found. Indexed and repaired
// files are counts because they need no decision; every flagged file is named,
// because "3 conflicting" leaves the user nothing to act on and is
// indistinguishable from a scan that never ran.
//
// ponytail: at most 10 names per group. A vault-wide move would otherwise bury
// the rest of startup; move the report behind /doctor if that limit ever hides
// something the user needed.
func reportVaultScan(out io.Writer, scan notes.VaultScan) {
	if len(scan.Added)+len(scan.Repaired)+len(scan.Missing)+len(scan.Conflicting) == 0 {
		return
	}
	fmt.Fprintf(out, "vault reconcile: %d indexed, %d repaired, %d missing, %d conflicting\n",
		len(scan.Added), len(scan.Repaired), len(scan.Missing), len(scan.Conflicting))
	reportVaultIssues(out, "missing", scan.Missing)
	reportVaultIssues(out, "conflicting", scan.Conflicting)
}

func reportVaultIssues(out io.Writer, label string, issues []notes.VaultIssue) {
	const shown = 10
	for index, issue := range issues {
		if index == shown {
			fmt.Fprintf(out, "  … and %d more %s\n", len(issues)-shown, label)
			return
		}
		fmt.Fprintf(out, "  %s: %s — %s\n", label, issue.Path, issue.Reason)
	}
}

// processContext lives as long as the process. It carries no deadline on
// purpose: a session may stay open for hours, and the only thing that should
// be bounded is a single request (chat.TurnTimeout). SIGINT and SIGTERM cancel
// it so Ctrl+C, or the Ink client killing its engine child, stops in-flight
// turns instead of leaving the work running behind a dead terminal.
func processContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// buildDispatcher registers every action type the model is allowed to
// invoke (see ai/prompt.go for the schema advertised to the model).
// Add a handler here whenever a new action type is added to that prompt.
func buildDispatcher(notesSvc *notes.Service, bookResolver *books.Resolver) *tools.Dispatcher {
	d := tools.NewDispatcher()

	d.Register("create_note", func(ctx context.Context, a ai.Action) (string, error) {
		n, created, err := notesSvc.CreateNote(ctx, a.Title, a.Content, a.Folder, a.Tags)
		if err != nil {
			return "", err
		}
		path := utils.RelVault(notesSvc.VaultPath(), n.Path)
		if !created {
			return fmt.Sprintf("Note %q already exists at %s (left unchanged)", n.Title, path), nil
		}
		return fmt.Sprintf("Created note %q at %s", n.Title, path), nil
	})

	d.Register("create_task", func(ctx context.Context, a ai.Action) (string, error) {
		n, created, err := notesSvc.CreateTask(ctx, a.Title, a.Content, a.Folder)
		if err != nil {
			return "", err
		}
		path := utils.RelVault(notesSvc.VaultPath(), n.Path)
		if !created {
			return fmt.Sprintf("Task %q already exists at %s (left unchanged)", n.Title, path), nil
		}
		return fmt.Sprintf("Created task %q at %s", n.Title, path), nil
	})

	d.Register("create_book", func(ctx context.Context, a ai.Action) (string, error) {
		metadata, err := bookResolver.ResolveWithFallback(ctx, a.Title, a.ISBN, a.Authors, a.Genres)
		if err != nil {
			return "", err
		}
		n, created, err := notesSvc.CreateBook(ctx, metadata, a.Folder, time.Now())
		if err != nil {
			return "", err
		}
		path := utils.RelVault(notesSvc.VaultPath(), n.Path)
		if !created {
			return fmt.Sprintf("Book %q (note %d) already exists at %s (left unchanged)", n.Title, n.ID, path), nil
		}
		if metadata.Source == "unresolved" {
			return fmt.Sprintf("Created book %q (note %d) at %s. Its metadata is unknown; Athena did not guess.", n.Title, n.ID, path), nil
		}
		genres := "none listed"
		if len(metadata.Genres) > 0 {
			genres = strings.Join(metadata.Genres, ", ")
		}
		return fmt.Sprintf("Created book %q (note %d) at %s with metadata from %s; catalog genres: %s", n.Title, n.ID, path, metadata.Source, genres), nil
	})

	d.Register("update_book_metadata", func(ctx context.Context, a ai.Action) (string, error) {
		if err := notesSvc.UpdateBookMetadata(ctx, a.NoteID, a.Authors, a.Genres); err != nil {
			return "", err
		}
		return fmt.Sprintf("Updated factual metadata for book %d", a.NoteID), nil
	})

	d.Register("finish_book", func(ctx context.Context, a ai.Action) (string, error) {
		if err := notesSvc.FinishBook(ctx, a.NoteID, time.Now()); err != nil {
			return "", err
		}
		return fmt.Sprintf("Recorded that book %d was finished at the local time", a.NoteID), nil
	})

	d.Register("ensure_folders", func(_ context.Context, a ai.Action) (string, error) {
		folders, err := notesSvc.EnsureFolders(a.Paths)
		if err != nil {
			return "", err
		}
		if len(folders) == 0 {
			return "No new folders needed", nil
		}
		return fmt.Sprintf("Ensured folders: %s", strings.Join(folders, ", ")), nil
	})

	d.Register("move_note", func(ctx context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("move_note requires note_id")
		}
		n, err := notesSvc.MoveNote(ctx, a.NoteID, a.Folder)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Moved note %q to %s", n.Title, utils.RelVault(notesSvc.VaultPath(), n.Path)), nil
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

	d.Register("append_note", func(ctx context.Context, a ai.Action) (string, error) {
		if err := notesSvc.AppendNote(ctx, a.NoteID, a.Content); err != nil {
			return "", err
		}
		return fmt.Sprintf("Appended to note %d", a.NoteID), nil
	})

	d.Register("replace_section", func(ctx context.Context, a ai.Action) (string, error) {
		if err := notesSvc.ReplaceSection(ctx, a.NoteID, a.Section, a.ExpectedContent, a.Content); err != nil {
			return "", err
		}
		return fmt.Sprintf("Updated section %q in note %d", a.Section, a.NoteID), nil
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

	d.Register("create_folder", func(_ context.Context, a ai.Action) (string, error) {
		folder, err := notesSvc.CreateFolder(a.Folder)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Created Folder %s", folder), nil
	})

	d.Register("link_folders", func(_ context.Context, a ai.Action) (string, error) {
		folders, err := notesSvc.LinkFolders(a.Folders)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Linked folders in Obsidian: %s", strings.Join(folders, ", ")), nil
	})

	d.Register("unlink_folders", func(_ context.Context, a ai.Action) (string, error) {
		folders, err := notesSvc.UnlinkFolders(a.Folders)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Unlinked folders in Obsidian: %s", strings.Join(folders, ", ")), nil
	})

	d.Register("set_folder_colors", func(_ context.Context, a ai.Action) (string, error) {
		styles, err := notesSvc.AddFolderGraphColors(a.Folder, a.IncludeChildren, a.Color)
		if err != nil {
			return "", err
		}
		// Report the color each orb actually ended up with. "Done" hides the
		// case where an existing user color was kept instead of applied.
		described := make([]string, 0, len(styles))
		for _, style := range styles {
			described = append(described, fmt.Sprintf("%s %s", style.Folder, style.Color))
		}
		return fmt.Sprintf("Set Obsidian graph colors: %s", strings.Join(described, ", ")), nil
	})

	// "Add X to the graph" is one action, not mkdir: a folder without an index
	// note is not a node in Obsidian's graph, so the user would see nothing.
	d.Register("create_graph_folder", func(_ context.Context, a ai.Action) (string, error) {
		style, err := notesSvc.AddFolderToGraph(a.Folder, a.Color)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Added %s to the Obsidian graph: folder, index note %s.md, orb color %s", style.Folder, style.Folder, style.Color), nil
	})

	d.Register("set_graph_node_size", func(_ context.Context, a ai.Action) (string, error) {
		if err := notesSvc.SetGraphNodeSizeMultiplier(a.NodeSizeMultiplier); err != nil {
			return "", err
		}
		return fmt.Sprintf("Set the Obsidian graph node size to %.2gx", a.NodeSizeMultiplier), nil
	})

	d.Register("list_folders", func(_ context.Context, _ ai.Action) (string, error) {
		folders, err := notesSvc.ListFolders()
		if err != nil {
			return "", err
		}
		if len(folders) == 0 {
			return "No folders yet", nil
		}
		return fmt.Sprintf("Folders: %s", strings.Join(folders, ", ")), nil
	})

	d.Register("folder_exists", func(_ context.Context, a ai.Action) (string, error) {
		exists, err := notesSvc.FolderExists(a.Folder)
		if err != nil {
			return "", err
		}
		if exists {
			return fmt.Sprintf("Folder %s exists", a.Folder), nil
		}
		return fmt.Sprintf("Folder %s does not exist", a.Folder), nil
	})

	d.Register("delete_folder", func(_ context.Context, a ai.Action) (string, error) {
		if err := notesSvc.DeleteEmptyFolder(a.Folder); err != nil {
			return "", err
		}
		return fmt.Sprintf("Deleted folder %s", a.Folder), nil
	})

	d.Register("rename_folder", func(_ context.Context, a ai.Action) (string, error) {
		newFolder, err := notesSvc.RenameFolder(a.Folder, a.NewFolder)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Renamed folder to %s", newFolder), nil
	})

	d.Register("move_folder", func(_ context.Context, a ai.Action) (string, error) {
		newFolder, err := notesSvc.MoveFolder(a.Folder, a.NewFolder)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Moved folder to %s", newFolder), nil
	})

	d.Register("rename_note", func(_ context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("rename_note requires note_id")
		}
		n, err := notesSvc.RenameNote(a.NoteID, a.Title)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Renamed note %d to %q", n.ID, n.Title), nil
	})

	d.Register("duplicate_note", func(ctx context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("duplicate_note requires note_id")
		}
		n, err := notesSvc.DuplicateNote(ctx, a.NoteID, a.Title, a.Folder)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Duplicated note %d as %q at %s", a.NoteID, n.Title, utils.RelVault(notesSvc.VaultPath(), n.Path)), nil
	})

	d.Register("trash_note", func(_ context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("trash_note requires note_id")
		}
		n, err := notesSvc.TrashNote(a.NoteID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Moved %q to trash", n.Title), nil
	})

	d.Register("restore_note", func(_ context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("restore_note requires note_id")
		}
		n, err := notesSvc.RestoreNote(a.NoteID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Restored %q from trash", n.Title), nil
	})

	d.Register("archive_note", func(_ context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("archive_note requires note_id")
		}
		n, err := notesSvc.ArchiveNote(a.NoteID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Archived %q", n.Title), nil
	})

	d.Register("unarchive_note", func(_ context.Context, a ai.Action) (string, error) {
		if a.NoteID == 0 {
			return "", fmt.Errorf("unarchive_note requires note_id")
		}
		n, err := notesSvc.UnarchiveNote(a.NoteID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Unarchived %q", n.Title), nil
	})

	// Which writes have invariants, and what they are, belongs to the notes
	// domain; the composition root only wires the callback in.
	for _, actionType := range notes.VerifiedWriteActions() {
		d.RegisterVerifier(actionType, notesSvc.VerifyWrite)
	}

	return d
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "failed to %s: %v\n", step, err)
	os.Exit(1)
}
