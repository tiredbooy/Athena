package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/books"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/notes"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/tools"
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
	notes       *notes.Service
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

// SetNotes gives the loop the vault service so `/doctor` can read index health
// and `/reindex` can rebuild it. Only user commands reach it: the model's own
// vault writes go through the dispatcher, which is what keeps the expensive,
// whole-vault rebuild out of any action a model could propose.
//
// Optional, like the setters above: a Loop without it still answers every turn,
// and `/doctor` simply omits the index-health line.
func (l *Loop) SetNotes(service *notes.Service) {
	l.notes = service
}

// isListingRequest matches complete, exact phrasings of exactly one request:
// "show me every note in the vault". Matching is whole-string on purpose. A
// prefix or keyword test would fire on a compound request ("list my notes and
// delete the old ones"), skip the model, and silently drop the rest of the
// sentence — so the precision is the feature, not a limitation.
//
// R-04: only read-only, unambiguous, self-contained phrasings belong here, and
// every entry must name notes. "what's in my vault" is deliberately absent:
// catalogText reports notes only, so a vault-wide question would get an answer
// that reads complete and is not.
func isListingRequest(input string) bool {
	// Fields collapses stray inner whitespace so sloppy typing still hits an
	// exact entry; it cannot widen what matches.
	switch strings.Trim(strings.Join(strings.Fields(strings.ToLower(input)), " "), ".?!") {
	case "what notes do i have",
		"what notes are in my vault",
		"list my notes", "list all my notes", "list notes", "list all notes",
		"show my notes", "show all my notes", "show notes", "show all notes",
		"show me my notes", "show me all my notes",
		"my notes", "all my notes":
		return true
	}
	return false
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

// describeActions fills each action's engine-generated Summary before the plan
// crosses a UI boundary. The engine already knows how to name an action — it
// prints exactly this in the review text — so every client should not have to
// rebuild that switch. A model-supplied Summary is discarded: display text is
// the engine's to write.
func describeActions(actions []ai.Action) []ai.Action {
	described := make([]ai.Action, 0, len(actions))
	for _, action := range actions {
		summary := actionVerb(action)
		if target := actionTarget(action); target != "" {
			summary += " " + target
		}
		action.Summary = summary
		described = append(described, action)
	}
	return described
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
