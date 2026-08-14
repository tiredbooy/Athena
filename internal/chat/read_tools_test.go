package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

func TestReadToolSchemasEncodeRequiredFieldsAsArrays(t *testing.T) {
	for _, tool := range readToolDefinitions() {
		if tool.Function.Parameters["type"] != "object" {
			t.Fatalf("%s parameters type = %v, want object", tool.Function.Name, tool.Function.Parameters["type"])
		}
		properties, ok := tool.Function.Parameters["properties"].(map[string]any)
		if !ok || properties == nil {
			t.Fatalf("%s properties schema is %T, want a non-nil object", tool.Function.Name, tool.Function.Parameters["properties"])
		}
		required, ok := tool.Function.Parameters["required"]
		if !ok {
			continue
		}
		if _, ok := required.([]string); !ok {
			t.Fatalf("%s required schema is %T, want []string", tool.Function.Name, required)
		}
	}
}

func TestTypedProposalSchemaRequiresActionSpecificFolderFields(t *testing.T) {
	schema := proposalActionSchema([]string{"create_folder", "ensure_folders"})
	variants, ok := schema["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("proposal variants = %#v", schema["oneOf"])
	}
	requiredByType := make(map[string]map[string]bool)
	for _, raw := range variants {
		variant := raw.(map[string]any)
		properties := variant["properties"].(map[string]any)
		typeSchema := properties["type"].(map[string]any)
		actionType := typeSchema["enum"].([]string)[0]
		required := make(map[string]bool)
		for _, field := range variant["required"].([]string) {
			required[field] = true
		}
		requiredByType[actionType] = required
	}
	if !requiredByType["create_folder"]["folder"] || requiredByType["create_folder"]["paths"] {
		t.Fatalf("create_folder requirements = %v", requiredByType["create_folder"])
	}
	if !requiredByType["ensure_folders"]["paths"] || requiredByType["ensure_folders"]["folder"] {
		t.Fatalf("ensure_folders requirements = %v", requiredByType["ensure_folders"])
	}
}

func TestBookOrganizationNarrowsProposalActions(t *testing.T) {
	got := actionTypesForGoal("organize my reading books into genre folders and record the author")
	joined := strings.Join(got, ",")
	for _, required := range []string{"create_book", "update_book_metadata", "ensure_folders", "move_note"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("actions %v omit %s", got, required)
		}
	}
	if strings.Contains(joined, "trash_note") || strings.Contains(joined, "set_graph_node_size") {
		t.Fatalf("book task received unrelated actions: %v", got)
	}
}

func TestPlainFallbackReceivesNarrowedRequiredFieldContract(t *testing.T) {
	message := taskActionContractMessage([]string{"create_folder", "ensure_folders"})
	if !strings.Contains(message.Content, "create_folder requires folder") || !strings.Contains(message.Content, "ensure_folders requires paths") {
		t.Fatalf("contract = %s", message.Content)
	}
	if strings.Contains(message.Content, "trash_note") {
		t.Fatalf("contract included an unrelated action: %s", message.Content)
	}
}

func TestOllamaToolFailureDoesNotUseRetryBudget(t *testing.T) {
	provider := &failingToolProvider{}
	session := &Session{loop: &Loop{ai: provider}}
	_, err := session.chatWithRetry(t.Context(), []models.Message{{Role: "user", Content: "hi"}}, []models.ToolDefinition{{Type: "function"}}, nil, false)
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

func TestRejectedToolHistoryFallsBackWithCollectedReadFacts(t *testing.T) {
	provider := &rejectingToolHistoryProvider{}
	session := &Session{loop: &Loop{ai: provider}}

	got, err := session.runReadToolLoop(t.Context(), []models.Message{{Role: "user", Content: "organize my books"}}, nil)
	if err != nil || got != "fallback using folder inventory" {
		t.Fatalf("fallback = %q, err=%v", got, err)
	}
	if provider.calls != 3 || session.nativeToolsDisabledModel != "test" {
		t.Fatalf("calls=%d disabled=%q", provider.calls, session.nativeToolsDisabledModel)
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

func TestMutationPlanningUsesProviderRequiredToolMode(t *testing.T) {
	provider := &requiredDecisionProvider{}
	session := &Session{loop: &Loop{ai: provider}}

	result, err := session.runReadToolLoopStateWithPolicy(t.Context(), []models.Message{{Role: "user", Content: "organize my books"}}, nil, true)
	if err != nil {
		t.Fatalf("run required decision loop: %v", err)
	}
	_, actions := ai.ExtractActions(result.Content)
	if provider.requiredCalls != 1 || provider.ordinaryCalls != 0 {
		t.Fatalf("required calls=%d ordinary calls=%d", provider.requiredCalls, provider.ordinaryCalls)
	}
	if len(actions) != 1 || actions[0].Type != "ensure_folders" {
		t.Fatalf("actions = %+v", actions)
	}
}

func TestNativeClarificationBecomesProviderNeutralQuestion(t *testing.T) {
	message := models.Message{Role: "assistant", ToolCalls: []models.ToolCall{{
		ID: "call-question", Type: "function", Function: models.ToolCallFunction{
			Name: "request_clarification", Arguments: json.RawMessage(`{"question":"Did you mean Project Hail Mary?"}`),
		},
	}}}

	content, decisionTool, ok := decisionToolContentWithType(message)
	if !ok || decisionTool != "request_clarification" || content != "Did you mean Project Hail Mary?" || !onlyDecisionTools(message.ToolCalls) {
		t.Fatalf("content=%q tool=%q ok=%v", content, decisionTool, ok)
	}
}

func TestNativeFinishBecomesProviderNeutralAnswer(t *testing.T) {
	message := models.Message{Role: "assistant", ToolCalls: []models.ToolCall{{
		ID: "call-finish", Type: "function", Function: models.ToolCallFunction{
			Name: "finish_run", Arguments: json.RawMessage(`{"message":"All requested changes were verified."}`),
		},
	}}}

	content, ok := decisionToolContent(message)
	if !ok || content != "All requested changes were verified." || !onlyDecisionTools(message.ToolCalls) {
		t.Fatalf("content=%q ok=%v", content, ok)
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

type rejectingToolHistoryProvider struct{ calls int }

func (p *rejectingToolHistoryProvider) Name() string        { return "Ollama" }
func (p *rejectingToolHistoryProvider) ChatModel() string   { return "test" }
func (p *rejectingToolHistoryProvider) SetChatModel(string) {}
func (p *rejectingToolHistoryProvider) ChatModels(_ context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (p *rejectingToolHistoryProvider) ChatWithToolsResult(_ context.Context, messages []models.Message, tools []models.ToolDefinition) (ai.ToolChatResult, error) {
	p.calls++
	switch p.calls {
	case 1:
		return ai.ToolChatResult{Message: models.Message{
			Role: "assistant",
			ToolCalls: []models.ToolCall{{ID: "call-folders", Type: "function", Function: models.ToolCallFunction{
				Name: "unsupported_read", Arguments: json.RawMessage(`{}`),
			}}},
		}}, nil
	case 2:
		return ai.ToolChatResult{}, errors.New("ollama tool chat returned status 400: invalid tool history")
	default:
		if len(tools) != 0 {
			return ai.ToolChatResult{}, errors.New("fallback still included native tool definitions")
		}
		foundReadFacts := false
		for _, message := range messages {
			if message.Role == "tool" || len(message.ToolCalls) > 0 {
				return ai.ToolChatResult{}, errors.New("fallback retained native tool protocol")
			}
			if message.Role == "system" && strings.Contains(message.Content, "ATHENA READ TOOL RESULT") && strings.Contains(message.Content, "unsupported_read") {
				foundReadFacts = true
			}
		}
		if !foundReadFacts {
			return ai.ToolChatResult{}, errors.New("fallback discarded collected read facts")
		}
		return ai.ToolChatResult{Message: models.Message{Role: "assistant", Content: "fallback using folder inventory"}}, nil
	}
}
func (p *rejectingToolHistoryProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}

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

type requiredDecisionProvider struct {
	requiredCalls int
	ordinaryCalls int
}

func (p *requiredDecisionProvider) Name() string        { return "ChatGPT subscription" }
func (p *requiredDecisionProvider) ChatModel() string   { return "test" }
func (p *requiredDecisionProvider) SetChatModel(string) {}
func (p *requiredDecisionProvider) ChatModels(context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (p *requiredDecisionProvider) ChatWithToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ai.ToolChatResult, error) {
	p.ordinaryCalls++
	return ai.ToolChatResult{}, errors.New("ordinary tool mode should not be used")
}
func (p *requiredDecisionProvider) ChatWithRequiredToolsResult(_ context.Context, _ []models.Message, tools []models.ToolDefinition) (ai.ToolChatResult, error) {
	p.requiredCalls++
	foundProposal := false
	foundQuestion := false
	foundFinish := false
	for _, tool := range tools {
		foundProposal = foundProposal || tool.Function.Name == "propose_actions"
		foundQuestion = foundQuestion || tool.Function.Name == "request_clarification"
		foundFinish = foundFinish || tool.Function.Name == "finish_run"
	}
	if !foundProposal || !foundQuestion || !foundFinish {
		return ai.ToolChatResult{}, errors.New("decision tools were not offered")
	}
	return ai.ToolChatResult{Message: models.Message{Role: "assistant", ToolCalls: []models.ToolCall{{
		ID: "call-actions", Type: "function", Function: models.ToolCallFunction{
			Name: "propose_actions", Arguments: json.RawMessage(`{"actions":[{"type":"ensure_folders","paths":["books/reading/science-fiction"]}]}`),
		},
	}}}}, nil
}
func (p *requiredDecisionProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}
