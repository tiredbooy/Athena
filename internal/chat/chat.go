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
)

const systemPrompt = `You are the user's second brain: a local assistant that helps them
capture notes and tasks and recall things they've written before. Be concise.
When relevant notes are provided below a message, ground your answer in them
and say so plainly if nothing relevant exists yet — don't invent notes.`

type Loop struct {
	ai        *ai.Client
	retrieval *retrieval.Service
}

func NewLoop(aiClient *ai.Client, retrievalSvc *retrieval.Service) *Loop {
	return &Loop{ai: aiClient, retrieval: retrievalSvc}
}

func (l *Loop) Run() {
	history := []models.Message{{Role: "system", Content: systemPrompt}}
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Type a message ('exit' to quit):")
	for {
		fmt.Print("> ")
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

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

		noteContext, err := l.retrieval.BuildContext(ctx, input, 4)
		if err != nil {
			fmt.Printf("(retrieval failed, answering without notes: %v)\n", err)
			noteContext = ""
		}

		turnMessage := input
		if noteContext != "" {
			turnMessage = fmt.Sprintf("%s\n\n%s", input, noteContext)
		}
		history = append(history, models.Message{Role: "user", Content: turnMessage})

		// Loading indicator: spin dots in a goroutine until the first
		// token actually arrives, so a slow model doesn't look frozen.
		stopLoading := make(chan struct{})
		firstToken := true
		start := time.Now()

		go func() {
			ticker := time.NewTicker(300 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopLoading:
					return
				case <-ticker.C:
					fmt.Printf("\rthinking... (%.0fs)   ", time.Since(start).Seconds())
				}
			}
		}()

		reply, err := l.ai.StreamChat(ctx, history, func(tok string) {
			if firstToken {
				close(stopLoading)
				fmt.Print("\r          \r") // wipe the "thinking..." line
				firstToken = false
			}
			fmt.Print(tok)
		})
		cancel()

		if firstToken {
			// Model errored before ever producing a token — loader is
			// still spinning, so stop it here too.
			close(stopLoading)
			fmt.Print("\r          \r")
		}

		if err != nil {
			fmt.Printf("error: %v\n", err)
			history = history[:len(history)-1]
			continue
		}

		history = append(history, models.Message{Role: "assistant", Content: reply})
		fmt.Println()
	}
}
