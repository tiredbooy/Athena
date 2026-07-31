package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/chat"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/notes"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/tools"
	"github.com/tiredbooy/internal/tui"
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

	retrievalSvc := retrieval.NewService(cfg.VaultPath, noteStore, chunkStore, client)
	notesSvc := notes.NewService(cfg.VaultPath, noteStore, chunkStore, client)

	dispatcher := buildDispatcher(notesSvc)

	fmt.Println("second-brain ready.")
	fmt.Printf("  vault: %s\n  db:    %s\n  model: %s\n\n", cfg.VaultPath, cfg.DBPath, cfg.ChatModel)

	loop := chat.NewLoop(client, retrievalSvc, dispatcher, cfg)
	session := chat.NewSession(loop)
	if err := tui.RunBubble(session.Submit, session.Clear); err != nil {
		fatal("run terminal UI", err)
	}
}

// buildDispatcher registers every action type the model is allowed to
// invoke (see ai/prompt.go for the schema advertised to the model).
// Add a handler here whenever a new action type is added to that prompt.
func buildDispatcher(notesSvc *notes.Service) *tools.Dispatcher {
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

	return d
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "failed to %s: %v\n", step, err)
	os.Exit(1)
}
