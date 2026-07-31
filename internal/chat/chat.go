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
	} else {
		fmt.Println()
		fmt.Println("No related memories found.")
	}

	turnMessage := input
	if ctxResult.Context != "" {
		turnMessage += "\n\n" + ctxResult.Context
	}

	*historyPtr = append(*historyPtr, models.Message{
		Role:    "user",
		Content: turnMessage,
	})

	// -------------------------------------------------------
	// Model
	// -------------------------------------------------------

	loader.Waiting()

	firstToken := true

	reply, err := l.ai.StreamChat(
		ctx,
		*historyPtr,
		func(tok string) {
			if firstToken {
				loader.Stop()
				firstToken = false
			}
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

	_, foundActions := ai.ExtractActions(reply)

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
	}

	// Keep the raw reply (including the fenced action block) in
	// history so the model retains awareness of what it already
	// did if the user follows up ("did that note get created?").
	*historyPtr = append(*historyPtr, models.Message{
		Role:    "assistant",
		Content: reply,
	})
}
