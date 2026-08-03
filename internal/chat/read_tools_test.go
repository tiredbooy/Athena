package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

func TestReadToolSchemasEncodeRequiredFieldsAsArrays(t *testing.T) {
	for _, tool := range readToolDefinitions() {
		required, ok := tool.Function.Parameters["required"]
		if !ok {
			continue
		}
		if _, ok := required.([]string); !ok {
			t.Fatalf("%s required schema is %T, want []string", tool.Function.Name, required)
		}
	}
}

func TestOllamaToolFailureDoesNotUseRetryBudget(t *testing.T) {
	provider := &failingToolProvider{}
	session := &Session{loop: &Loop{ai: provider}}
	_, err := session.chatWithRetry(t.Context(), []models.Message{{Role: "user", Content: "hi"}}, []models.ToolDefinition{{Type: "function"}}, nil)
	if err == nil {
		t.Fatal("expected tool failure")
	}
	if provider.calls != 1 {
		t.Fatalf("tool calls = %d, want 1 before fallback", provider.calls)
	}
}

type failingToolProvider struct{ calls int }

func (p *failingToolProvider) Name() string                                         { return "Ollama" }
func (p *failingToolProvider) ChatModel() string                                    { return "test" }
func (p *failingToolProvider) SetChatModel(string)                                  {}
func (p *failingToolProvider) ChatModels(_ context.Context) ([]ai.ModelInfo, error) { return nil, nil }
func (p *failingToolProvider) ChatWithToolsResult(_ context.Context, _ []models.Message, _ []models.ToolDefinition) (ai.ToolChatResult, error) {
	p.calls++
	return ai.ToolChatResult{}, errors.New("ollama tool chat returned status 500: unsupported")
}
func (p *failingToolProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}
