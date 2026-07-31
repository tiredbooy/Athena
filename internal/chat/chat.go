package chat

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/tools"
	"github.com/tiredbooy/internal/tui"
)

type Loop struct {
	ai         *ai.Client
	retrieval  *retrieval.Service
	dispatcher *tools.Dispatcher
}

func NewLoop(aiClient *ai.Client, retrievalSvc *retrieval.Service, dispatcher *tools.Dispatcher) *Loop {
	return &Loop{ai: aiClient, retrieval: retrievalSvc, dispatcher: dispatcher}
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
	fmt.Println("Type a message ('exit' to quit).")

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

		l.handleTurn(input, &history)
	}
}

// handleTurn runs one full retrieval -> model -> action-dispatch cycle.
// historyPtr lets this append/rollback without the caller juggling slices.
func (l *Loop) handleTurn(input string, historyPtr *[]models.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	loader := tui.NewLoader()
	loader.Start()

	// -------------------------------------------------------
	// Retrieval
	// -------------------------------------------------------

	retrievalStart := time.Now()

	ctxResult, err := l.retrieval.BuildContext(ctx, input, 4)

	retrievalTime := time.Since(retrievalStart)

	if err != nil {
		loader.Step("Vector search", retrievalTime)
		loader.Stop()

		fmt.Printf("\nRetrieval failed: %v\n", err)
		return
	}

	loader.Step("Vector search", retrievalTime)
	loader.Step("Context assembled", 0)

	if len(ctxResult.Catalog) > 0 {
		fmt.Println()
		fmt.Printf("Vault: %d note(s)\n", len(ctxResult.Catalog))
	}

	if len(ctxResult.Results) > 0 {
		fmt.Println()
		fmt.Println("Retrieved memories")

		for _, note := range ctxResult.Results {
			loader.Memory(fmt.Sprintf(
				"%s (%.0f%%)",
				note.Title,
				note.Similarity*100,
			))
		}
	} else if len(ctxResult.Catalog) == 0 {
		fmt.Println()
		fmt.Println("Vault is empty — no notes yet.")
	} else {
		fmt.Println()
		fmt.Println("No related memories found for this query.")
	}

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

	loader.Waiting()

	firstToken := true

	reply, err := l.ai.StreamChat(
		ctx,
		modelMessages,
		func(tok string) {
			if firstToken {
				loader.Stop()
				firstToken = false
			}
			// Live stream shows raw tokens (including action fences).
			// Dispatch below still parses unclosed fences from small models.
			fmt.Print(tok)
		},
	)

	if firstToken {
		loader.Stop()
	}

	if err != nil {
		fmt.Printf("\nError: %v\n", err)

		// Roll back the user turn we appended above so history
		// doesn't carry a dangling unanswered message.
		*historyPtr = (*historyPtr)[:len(*historyPtr)-1]
		return
	}

	fmt.Println()

	// -------------------------------------------------------
	// Tool / action dispatch
	// -------------------------------------------------------
	// Reuses ctx from the model call above. cancel() is deferred to the
	// end of this function (not called early), so dispatch still has a
	// live context as long as it finishes within the remaining timeout.

	cleaned, foundActions := ai.ExtractActions(reply)

	if len(foundActions) > 0 && l.dispatcher != nil {
		results := l.dispatcher.Run(ctx, foundActions)

		fmt.Println()
		for _, r := range results {
			if r.Err != nil {
				fmt.Printf("✗ %s failed: %v\n", r.Action.Type, r.Err)
				continue
			}
			fmt.Printf("✓ %s\n", r.Message)
		}

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
	}

	*historyPtr = append(*historyPtr, models.Message{
		Role:    "assistant",
		Content: reply,
	})
}
