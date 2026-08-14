package chat

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/books"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/tools"
	"github.com/tiredbooy/internal/tui"
)

type Loop struct {
	ai          ai.ChatProvider
	providers   map[string]ai.ChatProvider
	oauth       *ai.CodexOAuth
	xaiOAuth    *ai.XAIOAuth
	retrieval   *retrieval.Service
	dispatcher  *tools.Dispatcher
	bookCatalog *books.Resolver
	config      *config.Config
	credentials *config.CredentialStore
}

func NewLoop(chatProvider ai.ChatProvider, providers map[string]ai.ChatProvider, oauth *ai.CodexOAuth, retrievalSvc *retrieval.Service, dispatcher *tools.Dispatcher, cfg *config.Config) *Loop {
	return &Loop{ai: chatProvider, providers: providers, oauth: oauth, retrieval: retrievalSvc, dispatcher: dispatcher, config: cfg}
}

func (l *Loop) SetCredentialStore(credentials *config.CredentialStore) {
	l.credentials = credentials
}

func (l *Loop) SetXAIOAuth(oauth *ai.XAIOAuth) {
	l.xaiOAuth = oauth
}

func (l *Loop) SetBookCatalog(catalog *books.Resolver) {
	l.bookCatalog = catalog
}

// Run is the line-oriented fallback UI. It intentionally delegates every turn
// to Session so terminal and stdio/TypeScript clients share one agent policy.
func (l *Loop) Run() {
	session := NewSession(l)
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("Athena")
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("Ask anything. /models changes your model · exit quits.")

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}
		if input == "/help" {
			fmt.Println("Commands: /models, /model <number-or-name>, /doctor, /compact, /confirm, /cancel, /help, exit")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), TurnTimeout)
		loader := tui.NewLoader()
		loader.Start()
		reply, err := session.Submit(ctx, input, loader.Info, nil)
		cancel()
		loader.Stop()
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			continue
		}
		if strings.TrimSpace(reply) != "" {
			fmt.Println("\nAthena")
			fmt.Println(tui.RenderMarkdown(reply))
		}
	}
}

func isListingRequest(input string) bool {
	q := strings.Trim(strings.ToLower(strings.TrimSpace(input)), ".?!")
	return q == "what notes do i have" ||
		q == "list my notes" ||
		q == "show my notes" ||
		q == "list notes" || q == "show notes" || q == "my notes"
}

func catalogText(catalog []retrieval.CatalogEntry) string {
	var b strings.Builder
	if len(catalog) == 0 {
		b.WriteString("Your vault is empty.")
	} else {
		fmt.Fprintf(&b, "%d note", len(catalog))
		if len(catalog) != 1 {
			b.WriteByte('s')
		}
		b.WriteString(" in your vault:")
		for _, note := range catalog {
			folder := note.Folder
			if folder == "" {
				folder = "vault root"
			}
			fmt.Fprintf(&b, "\n• %s — %s", note.Title, folder)
		}
	}
	return b.String()
}

// runActionsWithStatus remains only as a compatibility path for plans created
// before resumable agent state was introduced. New plans execute through the
// agent driver's dispatcher boundary.
func (l *Loop) runActionsWithStatus(ctx context.Context, actions []ai.Action, status func(string)) string {
	if l.dispatcher == nil {
		return "I can't make changes because the action handler is unavailable."
	}
	progressCtx := tools.WithActionProgress(ctx, func(progress tools.ActionProgress) {
		if status != nil {
			status(actionProgressMessage(progress))
		}
	})
	results := l.dispatcher.RunBatch(progressCtx, actions, 4)
	var b strings.Builder
	for _, result := range results {
		if result.Err != nil {
			fmt.Fprintf(&b, "Could not %s: %v\n", result.Action.Type, result.Err)
			continue
		}
		fmt.Fprintf(&b, "✓ %s\n", result.Message)
	}
	return strings.TrimSpace(b.String())
}

func actionProgressMessage(progress tools.ActionProgress) string {
	target := actionTarget(progress.Action)
	if progress.State == "started" {
		return actionVerb(progress.Action) + " " + target
	}
	if progress.Error != nil {
		return "Could not " + strings.ToLower(actionVerb(progress.Action)) + " " + target + ": " + progress.Error.Error()
	}
	if progress.Message != "" {
		return progress.Message
	}
	return "Finished " + target
}

func actionVerb(action ai.Action) string {
	switch action.Type {
	case "create_note", "create_task", "create_book":
		return "Creating"
	case "update_note", "replace_section", "rename_note":
		return "Editing"
	case "append_note", "mark_done":
		return "Updating"
	case "update_book_metadata":
		return "Updating book metadata for"
	case "move_note", "move_folder":
		return "Moving"
	case "delete_folder", "trash_note":
		return "Removing"
	case "restore_note":
		return "Restoring"
	case "archive_note":
		return "Archiving"
	case "unarchive_note":
		return "Unarchiving"
	case "ensure_folders", "create_folder":
		return "Preparing"
	case "link_folders":
		return "Linking"
	case "unlink_folders":
		return "Unlinking"
	case "set_folder_colors":
		return "Coloring"
	case "set_graph_node_size":
		return "Resizing"
	case "finish_book":
		return "Finishing"
	default:
		return "Running"
	}
}

func actionTarget(action ai.Action) string {
	switch action.Type {
	case "create_note", "create_task", "create_book":
		kind := strings.TrimPrefix(action.Type, "create_")
		return fmt.Sprintf("%s %q", kind, action.Title)
	case "update_note", "append_note", "replace_section", "rename_note", "duplicate_note", "trash_note", "restore_note", "archive_note", "unarchive_note", "update_book_metadata":
		if action.NoteID != 0 {
			if action.Section != "" {
				return fmt.Sprintf("section %q in note %d", action.Section, action.NoteID)
			}
			return fmt.Sprintf("note %d", action.NoteID)
		}
	case "move_note":
		return fmt.Sprintf("note %d to %s", action.NoteID, action.Folder)
	case "create_folder", "delete_folder", "folder_exists":
		return fmt.Sprintf("folder %q", action.Folder)
	case "set_folder_colors":
		if action.IncludeChildren {
			return fmt.Sprintf("folder %q and its direct subfolders", action.Folder)
		}
		return fmt.Sprintf("folder %q", action.Folder)
	case "set_graph_node_size":
		return fmt.Sprintf("all graph nodes to %.2gx", action.NodeSizeMultiplier)
	case "ensure_folders":
		return "folders " + strings.Join(action.Paths, ", ")
	case "link_folders", "unlink_folders":
		return "folders " + strings.Join(action.Folders, ", ")
	case "move_folder":
		return fmt.Sprintf("folder %q", action.Folder)
	}
	return strings.ReplaceAll(action.Type, "_", " ")
}
