package chat

import (
	"context"
	"encoding/json"
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

func TestRejectedNativeToolsAreRememberedAndFallbackIsReused(t *testing.T) {
	provider := &rejectingToolProvider{}
	session := &Session{loop: &Loop{ai: provider}}

	got, err := session.runReadToolLoop(t.Context(), []models.Message{{Role: "user", Content: "read my note"}}, nil)
	if err != nil || got != "fallback answer" {
		t.Fatalf("fallback = %q, err=%v", got, err)
	}
	if provider.calls != 2 || session.nativeToolsDisabledModel != "test" {
		t.Fatalf("calls=%d disabled=%q", provider.calls, session.nativeToolsDisabledModel)
	}

	got, err = session.runReadToolLoop(t.Context(), []models.Message{{Role: "user", Content: "read another note"}}, nil)
	if err != nil || got != "fallback answer" || provider.calls != 3 {
		t.Fatalf("remembered fallback = %q, err=%v, calls=%d", got, err, provider.calls)
	}
}

func TestKnownUnsupportedNativeToolsSkipTheRejectedAttempt(t *testing.T) {
	provider := &knownPlainChatProvider{}
	session := &Session{loop: &Loop{ai: provider}}

	got, err := session.runReadToolLoop(t.Context(), []models.Message{{Role: "user", Content: "read my note"}}, nil)
	if err != nil || got != "prepared-context answer" {
		t.Fatalf("answer = %q, err=%v", got, err)
	}
	if provider.calls != 1 || session.nativeToolsDisabledModel != "test" {
		t.Fatalf("calls=%d disabled=%q", provider.calls, session.nativeToolsDisabledModel)
	}
}

func TestNativeActionProposalBecomesProviderNeutralDecision(t *testing.T) {
	provider := &actionProposalProvider{}
	session := &Session{loop: &Loop{ai: provider}}

	result, err := session.runReadToolLoopState(t.Context(), []models.Message{{Role: "user", Content: "create a note"}}, nil)
	if err != nil {
		t.Fatalf("run model loop: %v", err)
	}
	_, actions := ai.ExtractActions(result.Content)
	if len(actions) != 1 || actions[0].Type != "create_note" || actions[0].Title != "A useful title" {
		t.Fatalf("actions = %+v", actions)
	}
	last := result.Messages[len(result.Messages)-1]
	if len(last.ToolCalls) != 0 {
		t.Fatalf("unresolved proposal tool call remained in provider history: %+v", last.ToolCalls)
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

type rejectingToolProvider struct{ calls int }

func (p *rejectingToolProvider) Name() string        { return "Ollama" }
func (p *rejectingToolProvider) ChatModel() string   { return "test" }
func (p *rejectingToolProvider) SetChatModel(string) {}
func (p *rejectingToolProvider) ChatModels(_ context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (p *rejectingToolProvider) ChatWithToolsResult(_ context.Context, _ []models.Message, tools []models.ToolDefinition) (ai.ToolChatResult, error) {
	p.calls++
	if len(tools) > 0 {
		return ai.ToolChatResult{}, errors.New("ollama tool chat returned status 500: unsupported tools")
	}
	return ai.ToolChatResult{Message: models.Message{Role: "assistant", Content: "fallback answer"}}, nil
}
func (p *rejectingToolProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}

type knownPlainChatProvider struct{ calls int }

func (p *knownPlainChatProvider) Name() string        { return "Ollama" }
func (p *knownPlainChatProvider) ChatModel() string   { return "test" }
func (p *knownPlainChatProvider) SetChatModel(string) {}
func (p *knownPlainChatProvider) ChatModels(_ context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (p *knownPlainChatProvider) NativeToolSupport(context.Context) (ai.NativeToolSupport, error) {
	return ai.NativeToolSupport{Reason: "template lacks tools"}, nil
}
func (p *knownPlainChatProvider) ChatWithToolsResult(_ context.Context, _ []models.Message, tools []models.ToolDefinition) (ai.ToolChatResult, error) {
	p.calls++
	if len(tools) != 0 {
		return ai.ToolChatResult{}, errors.New("native tools should have been skipped")
	}
	return ai.ToolChatResult{Message: models.Message{Role: "assistant", Content: "prepared-context answer"}}, nil
}
func (p *knownPlainChatProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}

type actionProposalProvider struct{}

func (p *actionProposalProvider) Name() string        { return "Test" }
func (p *actionProposalProvider) ChatModel() string   { return "test" }
func (p *actionProposalProvider) SetChatModel(string) {}
func (p *actionProposalProvider) ChatModels(context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (p *actionProposalProvider) ChatWithToolsResult(_ context.Context, _ []models.Message, tools []models.ToolDefinition) (ai.ToolChatResult, error) {
	foundProposal := false
	for _, tool := range tools {
		if tool.Function.Name == "propose_actions" {
			foundProposal = true
		}
	}
	if !foundProposal {
		return ai.ToolChatResult{}, errors.New("propose_actions was not offered")
	}
	return ai.ToolChatResult{Message: models.Message{
		Role: "assistant",
		ToolCalls: []models.ToolCall{{ID: "call-1", Type: "function", Function: models.ToolCallFunction{
			Name:      "propose_actions",
			Arguments: json.RawMessage(`{"summary":"Ready","actions":[{"type":"create_note","title":"A useful title","content":"Body"}]}`),
		}}},
	}}, nil
}
func (p *actionProposalProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}
