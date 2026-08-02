package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/books"
	"github.com/tiredbooy/internal/chat"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/notes"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/tools"
	"github.com/tiredbooy/internal/tui"
	"github.com/tiredbooy/internal/utils"
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
	if cfg.ActiveProvider == "" || cfg.ActiveProvider == "ollama" {
		if err := client.EnsureRunning(ctx); err != nil {
			fatal("start ollama", err)
		}
	}

	embeddings := ai.EmbeddingProvider(client)
	if cfg.EmbeddingProvider.Type == "openai_compatible" {
		embeddings = ai.NewOpenAIEmbeddingProvider(cfg.EmbeddingProvider.Name, cfg.EmbeddingProvider.BaseURL, cfg.EmbeddingProvider.APIKeyEnv, cfg.EmbeddingProvider.Model)
	}
	retrievalSvc := retrieval.NewService(cfg.VaultPath, noteStore, chunkStore, embeddings)
	notesSvc := notes.NewService(cfg.VaultPath, noteStore, chunkStore, embeddings)
	if err := notesSvc.SyncFolderGraph(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: Obsidian folder graph is unavailable: %v\n", err)
	}

	bookResolver := books.NewResolver(storage.NewBookMetadataStore(db), nil)
	dispatcher := buildDispatcher(notesSvc, bookResolver)
	dispatcher.SetAuditLogger(storage.NewActionAuditStore(db))

	fmt.Println("second-brain ready.")
	fmt.Printf("  vault: %s\n  db:    %s\n  model: %s\n\n", cfg.VaultPath, cfg.DBPath, cfg.ChatModel)

	oauth, err := ai.LoadCodexOAuth()
	if err != nil {
		fatal("load OpenAI subscription credentials", err)
	}
	providers := map[string]ai.ChatProvider{"ollama": client}
	for _, provider := range cfg.Providers {
		if provider.Type == "openai_codex" {
			providers[providerID(provider.Name)] = ai.NewCodexProvider(oauth, provider.ChatModel)
		} else if provider.Type == "anthropic" {
			providers[providerID(provider.Name)] = ai.NewAnthropicProvider(provider.Name, provider.BaseURL, provider.APIKeyEnv, provider.ChatModel)
		} else {
			providers[providerID(provider.Name)] = ai.NewOpenAICompatibleProvider(provider.Name, provider.BaseURL, provider.APIKeyEnv, provider.ChatModel)
		}
	}
	activeProvider := providers[cfg.ActiveProvider]
	if activeProvider == nil {
		activeProvider = client
	}
	loop := chat.NewLoop(activeProvider, providers, oauth, retrievalSvc, dispatcher, cfg)
	session := chat.NewSession(loop)
	if err := tui.RunBubble(session.Submit, session.Clear,
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

func providerID(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var out strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-")
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
		if !created {
			return fmt.Sprintf("Note %q already exists at %s (left unchanged)", n.Title, n.Path), nil
		}
		return fmt.Sprintf("Created note %q at %s", n.Title, n.Path), nil
	})

	d.Register("create_task", func(ctx context.Context, a ai.Action) (string, error) {
		n, created, err := notesSvc.CreateTask(ctx, a.Title, a.Content, a.Folder)
		if err != nil {
			return "", err
		}
		if !created {
			return fmt.Sprintf("Task %q already exists at %s (left unchanged)", n.Title, n.Path), nil
		}
		return fmt.Sprintf("Created task %q at %s", n.Title, n.Path), nil
	})

	d.Register("create_book", func(ctx context.Context, a ai.Action) (string, error) {
		metadata, err := bookResolver.Resolve(ctx, a.Title, a.ISBN)
		if err != nil {
			return "", err
		}
		n, created, err := notesSvc.CreateBook(ctx, metadata, a.Folder, time.Now())
		if err != nil {
			return "", err
		}
		if !created {
			return fmt.Sprintf("Book %q already exists at %s (left unchanged)", n.Title, n.Path), nil
		}
		if metadata.Source == "unresolved" {
			return fmt.Sprintf("Created book %q. Its metadata is unknown; Athena did not guess.", n.Title), nil
		}
		return fmt.Sprintf("Created book %q with metadata from %s", n.Title, metadata.Source), nil
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
		return fmt.Sprintf("Moved note %q to %s", n.Title, n.Path), nil
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
		return fmt.Sprintf("Duplicated note %d as %q at %s", a.NoteID, n.Title, n.Path), nil
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

	registerWriteVerifiers(d, notesSvc)

	return d
}

func registerWriteVerifiers(d *tools.Dispatcher, notesSvc *notes.Service) {
	for _, actionType := range []string{
		"create_note", "create_task", "move_note", "update_note", "mark_done",
		"append_note", "replace_section",
		"rename_note", "duplicate_note", "trash_note", "restore_note", "archive_note", "unarchive_note",
		"create_folder", "ensure_folders", "delete_folder", "rename_folder", "move_folder",
	} {
		d.RegisterVerifier(actionType, func(ctx context.Context, action ai.Action) error {
			return verifyWrite(ctx, notesSvc, action)
		})
	}
}

func verifyWrite(_ context.Context, notesSvc *notes.Service, action ai.Action) error {
	// Index notes are a derived Obsidian view of the vault. Refresh them after
	// every structural write, never by asking the model to construct wikilinks.
	if err := notesSvc.SyncFolderGraph(); err != nil {
		return fmt.Errorf("refresh Obsidian folder graph: %w", err)
	}

	switch action.Type {
	case "create_folder":
		exists, err := notesSvc.FolderExists(action.Folder)
		if err != nil {
			return fmt.Errorf("verify create_folder: %w", err)
		}
		if !exists {
			return fmt.Errorf("verify create_folder: folder not found")
		}
		return nil
	case "ensure_folders":
		for _, folder := range action.Paths {
			exists, err := notesSvc.FolderExists(folder)
			if err != nil {
				return fmt.Errorf("verify ensure_folders: %w", err)
			}
			if !exists {
				return fmt.Errorf("verify ensure_folders: %q not found", folder)
			}
		}
		return nil
	case "delete_folder":
		exists, err := notesSvc.FolderExists(action.Folder)
		if err != nil {
			return fmt.Errorf("verify delete_folder: %w", err)
		}
		if exists {
			return fmt.Errorf("verify delete_folder: folder remains")
		}
		return nil
	}

	if action.Type == "create_note" || action.Type == "create_task" {
		path, err := utils.NotePath(notesSvc.VaultPath(), action.Folder, action.Title)
		if err != nil {
			return fmt.Errorf("build expected note path: %w", err)
		}
		note, err := notesSvc.GetNoteByPath(path)
		if err != nil {
			return fmt.Errorf("verify created note: %w", err)
		}
		if note == nil {
			return fmt.Errorf("verify created note: record not found")
		}
		if action.Type == "create_task" && note.Type != models.NoteTypeTask {
			return fmt.Errorf("verify created task: note type is %q", note.Type)
		}
		return nil
	}

	note, err := notesSvc.GetNote(action.NoteID)
	if err != nil {
		return fmt.Errorf("verify %s: %w", action.Type, err)
	}
	if note == nil {
		return fmt.Errorf("verify %s: note %d not found", action.Type, action.NoteID)
	}

	switch action.Type {
	case "move_note":
		expected, err := utils.NotePath(notesSvc.VaultPath(), action.Folder, note.Title)
		if err != nil {
			return fmt.Errorf("build expected note path: %w", err)
		}
		if note.Path != expected {
			return fmt.Errorf("verify move_note: expected %s, got %s", expected, note.Path)
		}
	case "update_note":
		if note.Content != action.Content {
			return fmt.Errorf("verify update_note: saved content differs")
		}
	case "append_note":
		if !strings.HasSuffix(strings.TrimSpace(note.Content), strings.TrimSpace(action.Content)) {
			return fmt.Errorf("verify append_note: appended content is missing")
		}
	case "replace_section":
		if !strings.Contains(note.Content, strings.TrimSpace(action.Content)) {
			return fmt.Errorf("verify replace_section: replacement content is missing")
		}
	case "mark_done":
		if note.Done != action.Done {
			return fmt.Errorf("verify mark_done: expected done=%t", action.Done)
		}
	case "rename_note":
		if note.Title != action.Title {
			return fmt.Errorf("verify rename_note: expected title %q", action.Title)
		}
	case "trash_note":
		if note.TrashedFrom == "" {
			return fmt.Errorf("verify trash_note: note is not marked as trashed")
		}
	case "restore_note":
		if note.TrashedFrom != "" {
			return fmt.Errorf("verify restore_note: note remains marked as trashed")
		}
	case "archive_note":
		if !note.Archived {
			return fmt.Errorf("verify archive_note: note is not marked as archived")
		}
	case "unarchive_note":
		if note.Archived {
			return fmt.Errorf("verify unarchive_note: note remains archived")
		}
	}
	return nil
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "failed to %s: %v\n", step, err)
	os.Exit(1)
}
