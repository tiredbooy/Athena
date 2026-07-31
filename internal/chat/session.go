package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

// Session is the UI-independent owner of one conversation. Callers provide a
// status callback; they decide whether to render it in a CLI, Bubble Tea, or a
// future frontend.
type Session struct {
	loop    *Loop
	history []models.Message
}

func NewSession(loop *Loop) *Session {
	return &Session{loop: loop, history: []models.Message{{Role: "system", Content: ai.SystemPrompt}}}
}

func (s *Session) Submit(ctx context.Context, input string, status func(string), onToken func(string)) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	if strings.HasPrefix(input, "/") {
		return s.command(ctx, input)
	}
	if actions, ok := folderActions(input); ok {
		reply := s.loop.runActions(ctx, actions)
		s.append(input, reply)
		return reply, nil
	}
	if isListingRequest(input) {
		if status != nil {
			status("Reading vault inventory")
		}
		catalog, err := s.loop.retrieval.Inventory()
		if err != nil {
			return "", fmt.Errorf("list notes: %w", err)
		}
		reply := catalogText(catalog)
		s.append(input, reply)
		return reply, nil
	}
	result, err := s.loop.retrieval.BuildContextWithProgress(ctx, input, 4, status)
	if err != nil {
		return "", fmt.Errorf("retrieve context: %w", err)
	}
	s.history = append(s.history, models.Message{Role: "user", Content: input})
	messages := append([]models.Message(nil), s.history...)
	if result.Context != "" {
		messages[len(messages)-1].Content = input + "\n\n" + result.Context
	}
	if status != nil {
		status(fmt.Sprintf("Planning with %s", s.loop.ai.ChatModel()))
	}
	thinking, writing := false, false
	raw, err := s.loop.ai.StreamChatWith(ctx, messages, ai.StreamCallbacks{
		OnThinking: func(string) {
			if !thinking && status != nil {
				thinking = true
				status("Reviewing the model's reasoning")
			}
		},
		OnToken: func(token string) {
			if !writing && status != nil {
				writing = true
				status("Writing a response")
			}
			if onToken != nil {
				onToken(token)
			}
		},
	})
	if err != nil {
		s.history = s.history[:len(s.history)-1]
		return "", err
	}
	cleaned, actions := ai.ExtractActions(raw)
	var report strings.Builder
	if cleaned != "" {
		report.WriteString(cleaned)
	}
	if len(actions) > 0 && s.loop.dispatcher != nil {
		if status != nil {
			status(fmt.Sprintf("Executing %d planned action(s)", len(actions)))
		}
		for _, r := range s.loop.dispatcher.RunBatch(ctx, actions, 4) {
			if report.Len() > 0 {
				report.WriteString("\n")
			}
			if r.Err != nil {
				fmt.Fprintf(&report, "Could not %s: %v", r.Action.Type, r.Err)
			} else {
				fmt.Fprintf(&report, "✓ %s", r.Message)
			}
		}
	}
	reply := strings.TrimSpace(report.String())
	if reply == "" {
		reply = "The model returned no visible answer. No vault changes were made; please try again."
	}
	s.history = append(s.history, models.Message{Role: "assistant", Content: reply})
	return reply, nil
}

func (s *Session) command(ctx context.Context, input string) (string, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", nil
	}
	switch fields[0] {
	case "/models":
		available, err := s.loop.ai.ChatModels(ctx)
		if err != nil {
			return "", fmt.Errorf("list models: %w", err)
		}
		if len(available) == 0 {
			return "No chat-capable Ollama models found.", nil
		}
		var out strings.Builder
		out.WriteString("Available chat models:")
		for i, model := range available {
			marker := " "
			if model.Name == s.loop.ai.ChatModel() {
				marker = "*"
			}
			fmt.Fprintf(&out, "\n%s %d. %s", marker, i+1, model.Name)
		}
		return out.String(), nil
	case "/model":
		if len(fields) != 2 {
			return "Usage: /model <number-or-name>", nil
		}
		available, err := s.loop.ai.ChatModels(ctx)
		if err != nil {
			return "", fmt.Errorf("list models: %w", err)
		}
		selected := fields[1]
		if index, err := strconv.Atoi(selected); err == nil {
			if index < 1 || index > len(available) {
				return "Model number is out of range. Run /models first.", nil
			}
			selected = available[index-1].Name
		}
		for _, model := range available {
			if model.Name == selected {
				s.loop.ai.SetChatModel(selected)
				s.loop.config.ChatModel = selected
				if err := s.loop.config.Save(); err != nil {
					return "", fmt.Errorf("save selected model: %w", err)
				}
				return fmt.Sprintf("Using chat model %s", selected), nil
			}
		}
		return fmt.Sprintf("Model %q is not available. Run /models first.", selected), nil
	default:
		return "Unknown command. Type /help to see available commands.", nil
	}
}

func (s *Session) Clear() { s.history = s.history[:1] }
func (s *Session) append(input, reply string) {
	s.history = append(s.history, models.Message{Role: "user", Content: input}, models.Message{Role: "assistant", Content: reply})
}
