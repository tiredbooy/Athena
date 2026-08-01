package chat

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/tools"
	"github.com/tiredbooy/internal/tui"
)

type Loop struct {
	ai         *ai.Client
	retrieval  *retrieval.Service
	dispatcher *tools.Dispatcher
	config     *config.Config
}

func NewLoop(aiClient *ai.Client, retrievalSvc *retrieval.Service, dispatcher *tools.Dispatcher, cfg *config.Config) *Loop {
	return &Loop{ai: aiClient, retrieval: retrievalSvc, dispatcher: dispatcher, config: cfg}
}

func (l *Loop) Run() {
	history := []models.Message{
		{
			Role:    "system",
			Content: ai.SystemPrompt,
		},
	}

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("🦉 Athena")
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
		if strings.HasPrefix(input, "/") {
			l.handleCommand(input)
			continue
		}

		l.handleTurn(input, &history)
	}
}

// handleTurn runs one full retrieval -> model -> action-dispatch cycle.
// historyPtr lets this append/rollback without the caller juggling slices.
func (l *Loop) handleTurn(input string, historyPtr *[]models.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), TurnTimeout)
	defer cancel()

	loader := tui.NewLoader()
	loader.Start()
	if actions, ok := folderActions(input); ok {
		loader.Stop()
		reply := l.runActions(ctx, actions)
		fmt.Println("\nAthena")
		fmt.Println(reply)
		*historyPtr = append(*historyPtr,
			models.Message{Role: "user", Content: input},
			models.Message{Role: "assistant", Content: reply},
		)
		return
	}
	if isBookMoveRequest(input) {
		catalog, err := l.retrieval.Inventory()
		if err != nil {
			loader.Stop()
			fmt.Printf("\nCould not inspect your vault: %v\n", err)
			return
		}
		if actions, ok := bookMoveActions(input, catalog); ok {
			loader.Stop()
			reply := l.runActions(ctx, actions)
			fmt.Println("\nAthena")
			fmt.Println(reply)
			*historyPtr = append(*historyPtr,
				models.Message{Role: "user", Content: input},
				models.Message{Role: "assistant", Content: reply},
			)
			return
		}
	}
	if isListingRequest(input) {
		catalog, err := l.retrieval.Inventory()
		loader.Stop()
		if err != nil {
			fmt.Printf("\nCould not list your notes: %v\n", err)
			return
		}
		reply := printCatalog(catalog)
		*historyPtr = append(*historyPtr,
			models.Message{Role: "user", Content: input},
			models.Message{Role: "assistant", Content: reply},
		)
		return
	}
	// -------------------------------------------------------
	// Retrieval
	// -------------------------------------------------------

	retrievalStart := time.Now()

	ctxResult, err := l.retrieval.BuildContextWithProgress(ctx, input, 4, loader.Info)

	retrievalTime := time.Since(retrievalStart)

	if err != nil {
		loader.Step("Vector search", retrievalTime)
		loader.Stop()

		fmt.Printf("\nRetrieval failed: %v\n", err)
		return
	}

	loader.Step("Vector search", retrievalTime)
	loader.Step("Context assembled", 0)
	// Keep chat history free of the injected vault context so follow-up
	// turns don't accumulate stale catalogs. The model still sees the
	// catalog for *this* turn via the messages we pass to StreamChat.
	*historyPtr = append(*historyPtr, models.Message{
		Role:    "user",
		Content: input,
	})

	modelMessages := make([]models.Message, len(*historyPtr))
	copy(modelMessages, *historyPtr)
	if ctxResult.Context != "" {
		// Attach retrieval context only to the latest user turn.
		last := len(modelMessages) - 1
		modelMessages[last].Content = input + "\n\n" + ctxResult.Context
	}

	// -------------------------------------------------------
	// Model
	// -------------------------------------------------------

	loader.Info("Thinking about your question")
	loader.Waiting()

	firstToken := true

	reply, err := l.ai.StreamChatWith(
		ctx,
		modelMessages,
		ai.StreamCallbacks{
			OnThinking: func(delta string) { loader.NoteReasoning(len(delta)) },
			OnToken: func(tok string) {
				if firstToken {
					loader.TransitionToReply()
					firstToken = false
				}
				loader.NoteStream(len(tok))
			},
		},
	)

	loader.Stop()

	if err != nil {
		fmt.Printf("\nError: %v\n", err)

		// Roll back the user turn we appended above so history
		// doesn't carry a dangling unanswered message.
		*historyPtr = (*historyPtr)[:len(*historyPtr)-1]
		return
	}

	// -------------------------------------------------------
	// Tool / action dispatch
	// -------------------------------------------------------
	// Reuses ctx from the model call above. cancel() is deferred to the
	// end of this function (not called early), so dispatch still has a
	// live context as long as it finishes within the remaining timeout.

	cleaned, foundActions := ai.ExtractActions(reply)
	display := cleaned

	if len(foundActions) > 0 && l.dispatcher != nil {
		results := l.dispatcher.RunBatch(ctx, foundActions, 4)

		// Prefer cleaned prose in history once actions ran, plus a short
		// machine summary of what actually happened so follow-ups work.
		var summary strings.Builder
		if cleaned != "" {
			summary.WriteString(cleaned)
			summary.WriteString("\n\n")
		}
		for _, r := range results {
			if r.Err != nil {
				summary.WriteString(fmt.Sprintf("[action %s failed: %v]\n", r.Action.Type, r.Err))
				continue
			}
			summary.WriteString(fmt.Sprintf("[action ok] %s\n", r.Message))
		}
		reply = strings.TrimSpace(summary.String())

		// The model's confirmation is only an intention. Show what the
		// dispatcher actually did so a rejected action (for example, deleting
		// a non-empty folder) is visible instead of looking like a no-op.
		var execution strings.Builder
		for _, r := range results {
			if r.Err != nil {
				fmt.Fprintf(&execution, "Could not %s: %v\n", r.Action.Type, r.Err)
				continue
			}
			fmt.Fprintf(&execution, "✓ %s\n", r.Message)
		}
		if execution.Len() > 0 {
			if display != "" {
				display += "\n\n"
			}
			display += strings.TrimSpace(execution.String())
		}
	}

	if display == "" {
		display = "The model returned no visible answer. No vault changes were made; please try again."
		if strings.TrimSpace(reply) == "" {
			reply = display
		}
	}
	fmt.Println("\nAthena")
	fmt.Println(tui.RenderMarkdown(display))

	*historyPtr = append(*historyPtr, models.Message{
		Role:    "assistant",
		Content: reply,
	})
}

func (l *Loop) handleCommand(input string) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return
	}

	switch fields[0] {
	case "/help":
		fmt.Println("Commands: /models, /model <number-or-name>, /help, exit")
	case "/models":
		l.printModels()
	case "/model":
		if len(fields) != 2 {
			l.printModels()
			return
		}
		l.selectModel(fields[1])
	default:
		fmt.Printf("Unknown command %q. Run /help.\n", fields[0])
	}
}

func isListingRequest(input string) bool {
	q := strings.Trim(strings.ToLower(strings.TrimSpace(input)), ".?!")
	return q == "what notes do i have" ||
		q == "list my notes" ||
		q == "show my notes" ||
		q == "list notes" || q == "show notes" || q == "my notes"
}

var (
	createFolderRequest = regexp.MustCompile(`(?i)^\s*(?:please\s+)?(?:create|make|add)\s+(?:a\s+|the\s+)?folder(?:\s+(?:called|named))?\s+(.+?)\s*[.!?]?\s*$`)
	deleteFolderRequest = regexp.MustCompile(`(?i)^\s*(?:please\s+)?(?:delete|remove)\s+(?:the\s+)?folder(?:\s+(?:called|named))?\s+(.+?)\s*[.!?]?\s*$`)
)

// folderActions handles only complete, explicit folder requests. This keeps
// the common filesystem commands dependable even when a small local model
// forgets to emit its action JSON; broader organizational requests still go
// through the model, where their intent can be interpreted with vault context.
func folderActions(input string) ([]ai.Action, bool) {
	for _, request := range []struct {
		re     *regexp.Regexp
		action string
	}{
		{createFolderRequest, "create_folder"},
		{deleteFolderRequest, "delete_folder"},
	} {
		match := request.re.FindStringSubmatch(input)
		if len(match) != 2 {
			continue
		}
		folder := strings.Trim(strings.TrimSpace(match[1]), "`\"'")
		if folder == "" || hasAdditionalFolderWork(folder) {
			return nil, false
		}
		return []ai.Action{{Type: request.action, Folder: folder}}, true
	}
	return nil, false
}

// hasAdditionalFolderWork keeps this fast path from taking a compound request
// such as "create a folder for work and move notes into it" away from the
// model, which has the context needed to handle all requested operations.
func hasAdditionalFolderWork(folder string) bool {
	folder = strings.ToLower(folder)
	return strings.HasPrefix(folder, "for ") ||
		strings.Contains(folder, " and ") ||
		strings.Contains(folder, " then ") ||
		strings.Contains(folder, " with ")
}

func printCatalog(catalog []retrieval.CatalogEntry) string {
	reply := catalogText(catalog)
	fmt.Println("\nAthena")
	fmt.Println(reply)
	return reply
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

// isBookMoveRequest recognizes a high-confidence organization request that
// should not rely on a small local model correctly producing tool JSON.
func isBookMoveRequest(input string) bool {
	q := strings.ToLower(input)
	return strings.Contains(q, "book") && strings.Contains(q, "folder") &&
		strings.Contains(q, "reading") && strings.Contains(q, "move")
}

func bookMoveActions(input string, catalog []retrieval.CatalogEntry) ([]ai.Action, bool) {
	queryWords := significantWords(input)
	bestID, bestScore, ties := int64(0), 0, 0
	for _, note := range catalog {
		score := 0
		for word := range significantWords(note.Title) {
			if queryWords[word] {
				score++
			}
		}
		if score > bestScore {
			bestID, bestScore, ties = note.ID, score, 1
		} else if score == bestScore && score > 0 {
			ties++
		}
	}
	// Two title words (or a distinctive short ID such as D3) avoids moving a
	// random note when the wording is ambiguous.
	if bestScore < 2 || ties != 1 {
		return nil, false
	}
	return []ai.Action{
		{Type: "ensure_folders", Paths: []string{"book/reading"}},
		{Type: "move_note", NoteID: bestID, Folder: "book/reading"},
	}, true
}

func significantWords(text string) map[string]bool {
	stop := map[string]bool{
		"a": true, "about": true, "and": true, "book": true, "folder": true,
		"for": true, "have": true, "i": true, "in": true, "make": true,
		"me": true, "move": true, "my": true, "note": true, "reading": true,
		"subfolder": true, "that": true, "the": true, "to": true, "want": true,
		"with": true,
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := make(map[string]bool, len(words))
	for _, word := range words {
		if !stop[word] && (len(word) >= 3 || (len(word) >= 2 && word[0] >= '0' && word[0] <= '9')) {
			out[word] = true
		}
	}
	return out
}

func (l *Loop) runActions(ctx context.Context, actions []ai.Action) string {
	if l.dispatcher == nil {
		return "I can't make changes because the action handler is unavailable."
	}
	results := l.dispatcher.RunBatch(ctx, actions, 4)
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

func (l *Loop) printModels() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := l.ai.ChatModels(ctx)
	if err != nil {
		fmt.Printf("Could not list Ollama models: %v\n", err)
		return
	}
	if len(models) == 0 {
		fmt.Println("No chat-capable Ollama models found.")
		return
	}
	current := l.ai.ChatModel()
	fmt.Println("Available chat models:")
	for i, model := range models {
		marker := " "
		if model.Name == current {
			marker = "*"
		}
		details := model.ParameterSize
		if details == "" {
			details = "local"
		}
		fmt.Printf("  %s %d. %s (%s)\n", marker, i+1, model.Name, details)
	}
	fmt.Println("Use /model <number-or-name> to switch.")
}

func (l *Loop) selectModel(choice string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := l.ai.ChatModels(ctx)
	if err != nil {
		fmt.Printf("Could not list Ollama models: %v\n", err)
		return
	}

	selected := ""
	if n, err := strconv.Atoi(choice); err == nil {
		if n >= 1 && n <= len(models) {
			selected = models[n-1].Name
		}
	} else {
		for _, model := range models {
			if model.Name == choice {
				selected = model.Name
				break
			}
		}
	}
	if selected == "" {
		fmt.Println("That model was not found. Run /models to see choices.")
		return
	}

	l.ai.SetChatModel(selected)
	if l.config != nil {
		l.config.ChatModel = selected
		if err := l.config.Save(); err != nil {
			fmt.Printf("Using %s for this session, but could not save the choice: %v\n", selected, err)
			return
		}
	}
	fmt.Printf("Chat model switched to %s.\n", selected)
}
