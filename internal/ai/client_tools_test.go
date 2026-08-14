package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/models"
)

func TestChatWithToolsSendsSchemaAndDecodesToolCall(t *testing.T) {
	client := NewClient("http://ollama.test", "test-model", "test-embed")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request models.MessageReq
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Stream || len(request.Tools) != 1 || request.Tools[0].Function.Name != "list_folders" {
			t.Fatalf("tool request = %+v", request)
		}
		if temperature, ok := request.Options["temperature"].(float64); !ok || temperature != 0.2 {
			t.Fatalf("temperature option = %#v", request.Options["temperature"])
		}
		body, err := json.Marshal(map[string]any{
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"type":     "function",
					"function": map[string]any{"name": "list_folders", "arguments": map[string]any{}},
				}},
			},
			"done": true,
		})
		if err != nil {
			t.Fatalf("encode response: %v", err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}

	response, err := client.ChatWithTools(context.Background(), []models.Message{{Role: "user", Content: "show folders"}}, []models.ToolDefinition{{
		Type: "function", Function: models.ToolFunction{Name: "list_folders", Parameters: map[string]any{"type": "object"}},
	}})
	if err != nil {
		t.Fatalf("chat with tools: %v", err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Function.Name != "list_folders" {
		t.Fatalf("tool calls = %+v", response.ToolCalls)
	}
}

func TestShouldThinkOnlyForReasoningModelNames(t *testing.T) {
	for _, model := range []string{"qwen3:8b", "deepseek-r1:7b", "local-thinking-model"} {
		if !shouldThink(model) {
			t.Fatalf("shouldThink(%q) = false", model)
		}
	}
	for _, model := range []string{"llama3.2:3b", "qwen2.5:7b"} {
		if shouldThink(model) {
			t.Fatalf("shouldThink(%q) = true", model)
		}
	}
}

func TestNativeToolSupportRejectsToollessTemplateEvenWhenAdvertised(t *testing.T) {
	client := NewClient("http://ollama.test", "test-model", "test-embed")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/show" {
			t.Fatalf("path = %s, want /api/show", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"capabilities":["tools"],"template":"{{ .Prompt }}"}`)), Header: make(http.Header)}, nil
	})}

	support, err := client.NativeToolSupport(context.Background())
	if err != nil || support.Available || support.Reason == "" {
		t.Fatalf("support = %+v, err=%v", support, err)
	}
}

func TestOllamaMessagesKeepLaterSystemContextInTurnOrder(t *testing.T) {
	messages := []models.Message{
		{Role: "system", Content: "base instructions"},
		{Role: "user", Content: "organize my books"},
		{Role: "assistant", Content: "I need the folder inventory"},
		{Role: "system", Content: "verified folder result"},
		{Role: "user", Content: "continue"},
	}

	normalized := ollamaMessages(messages)
	if len(normalized) != 4 {
		t.Fatalf("messages = %d, want 4 after adjacent user context is coalesced", len(normalized))
	}
	if normalized[0].Role != "system" || !strings.Contains(normalized[0].Content, "base instructions") || strings.Contains(normalized[0].Content, "verified folder result") {
		t.Fatalf("leading system message = %+v", normalized[0])
	}
	for index, message := range normalized[1:] {
		if message.Role == "system" {
			t.Fatalf("message %d retained a non-leading system role", index+1)
		}
	}
	if normalized[1].Content != "organize my books" || normalized[2].Content != "I need the folder inventory" || normalized[3].Role != "user" || !strings.Contains(normalized[3].Content, "verified folder result") || !strings.Contains(normalized[3].Content, "continue") {
		t.Fatalf("non-system message order changed: %+v", normalized)
	}
}

func TestOllamaMessagesTurnTrailingObservationIntoContinuationQuery(t *testing.T) {
	messages := []models.Message{
		{Role: "system", Content: "base instructions"},
		{Role: "user", Content: "organize my books"},
		{Role: "assistant", Content: "proposed and executed move"},
		{Role: "system", Content: "[ATHENA VERIFIED EXECUTION OBSERVATION]\nmove_note succeeded"},
	}

	normalized := ollamaMessages(messages)
	if len(normalized) != 4 {
		t.Fatalf("messages = %d, want 4", len(normalized))
	}
	if normalized[0].Role != "system" || strings.Contains(normalized[0].Content, "move_note succeeded") {
		t.Fatalf("leading system message incorrectly absorbed trailing observation: %+v", normalized[0])
	}
	last := normalized[len(normalized)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "NOT USER-AUTHORED") || !strings.Contains(last.Content, "move_note succeeded") {
		t.Fatalf("trailing continuation = %+v", last)
	}
	if normalized[1].Role != "user" || normalized[2].Role != "assistant" {
		t.Fatalf("conversation order changed: %+v", normalized)
	}
}

func TestOllamaMessagesAlwaysIncludeTemplateQuery(t *testing.T) {
	normalized := ollamaMessages([]models.Message{{Role: "system", Content: "answer concisely"}})
	if len(normalized) != 2 || normalized[0].Role != "system" || normalized[1].Role != "user" {
		t.Fatalf("normalized messages = %+v", normalized)
	}
}

func TestOllamaMessagesSeparateRepeatedAssistantPlansWithObservations(t *testing.T) {
	messages := []models.Message{
		{Role: "system", Content: "base instructions"},
		{Role: "user", Content: "add two books"},
		{Role: "assistant", Content: "first action plan"},
		{Role: "system", Content: "first action succeeded"},
		{Role: "assistant", Content: "second action plan"},
		{Role: "system", Content: "second action succeeded; evaluate the goal"},
	}

	normalized := ollamaMessages(messages)
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1].Role == "assistant" && normalized[index].Role == "assistant" {
			t.Fatalf("adjacent assistant messages at %d: %+v", index, normalized)
		}
		if normalized[index].Role == "system" {
			t.Fatalf("non-leading system message at %d: %+v", index, normalized)
		}
	}
	roles := make([]string, len(normalized))
	for index, message := range normalized {
		roles[index] = message.Role
	}
	if got := strings.Join(roles, ","); got != "system,user,assistant,user,assistant,user" {
		t.Fatalf("roles = %s", got)
	}
}

func TestPlainOllamaPlanningDisablesPrivateThinking(t *testing.T) {
	client := NewClient("http://ollama.test", "qwen3-thinking", "test-embed")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request models.MessageReq
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Think {
			t.Fatal("plain tool-less planning enabled private thinking")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"message":{"role":"assistant","content":"visible plan"},"done":true}`)), Header: make(http.Header)}, nil
	})}

	result, err := client.ChatWithToolsResult(t.Context(), []models.Message{{Role: "user", Content: "add a book"}}, nil)
	if err != nil || result.Message.Content != "visible plan" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestOllamaContextOverflowExpandsAndCachesSmallestWindow(t *testing.T) {
	client := NewClient("http://ollama.test", "large-context-model", "test-embed")
	var contextSizes []int
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request models.MessageReq
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		contextSize, _ := request.Options["num_ctx"].(float64)
		contextSizes = append(contextSizes, int(contextSize))
		if len(contextSizes) == 1 {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`{"error":"request (5082 tokens) exceeds the available context size (4096 tokens), try increasing it"}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"message":{"role":"assistant","content":"planned"},"done":true}`)),
			Header:     make(http.Header),
		}, nil
	})}

	for range 2 {
		result, err := client.ChatWithToolsResult(t.Context(), []models.Message{{Role: "user", Content: "add a book"}}, nil)
		if err != nil || result.Message.Content != "planned" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if len(contextSizes) != 3 {
		t.Fatalf("request count = %d, want overflow + retry + cached request", len(contextSizes))
	}
	if contextSizes[0] != 0 || contextSizes[1] != 8192 || contextSizes[2] != 8192 {
		t.Fatalf("context sizes = %v, want [0 8192 8192]", contextSizes)
	}
}

func TestOllamaContextExpansionRefusesUnboundedGrowth(t *testing.T) {
	client := NewClient("http://ollama.test", "test-model", "test-embed")
	if client.expandContextFor("test-model", "request (20000 tokens) exceeds the available context size (4096 tokens)") {
		t.Fatal("oversized request unexpectedly increased local context beyond the safety cap")
	}
	if options := client.localChatOptions("test-model"); options["num_ctx"] != nil {
		t.Fatalf("context option was cached after rejected expansion: %#v", options)
	}
}

func TestOllamaErrorDetailUnwrapsTemplateMessage(t *testing.T) {
	body := []byte(`{"error":"{\"error\":{\"code\":400,\"message\":\"System message must be at the beginning.\",\"type\":\"invalid_request_error\"}}"}`)
	if got := ollamaErrorDetail(body); got != "System message must be at the beginning." {
		t.Fatalf("detail = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
