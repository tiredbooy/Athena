package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/models"
)

func TestAnthropicProviderBuildsNativeMessagesRequest(t *testing.T) {
	provider := NewAnthropicProvider("anthropic", "https://api.anthropic.test/v1/", "ATHENA_TEST_ANTHROPIC_KEY", "claude-sonnet-4-5")
	// An explicitly set key must win over the environment so switching providers
	// mid-session does not require the user to export anything.
	t.Setenv("ATHENA_TEST_ANTHROPIC_KEY", "")
	provider.SetAPIKey("set-key")
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != "https://api.anthropic.test/v1/messages" {
			t.Fatalf("request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("x-api-key") != "set-key" || request.Header.Get("anthropic-version") != "2023-06-01" {
			t.Fatalf("headers = %v", request.Header)
		}
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			System    string `json:"system"`
			Messages  []struct {
				Role    string `json:"role"`
				Content []struct {
					Type      string          `json:"type"`
					Text      string          `json:"text"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
					ToolUseID string          `json:"tool_use_id"`
					Content   string          `json:"content"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "claude-sonnet-4-5" || body.MaxTokens != 4096 {
			t.Fatalf("model=%q max_tokens=%d", body.Model, body.MaxTokens)
		}
		// System turns are hoisted out of the message list into the top-level
		// "system" field, joined in order.
		if body.System != "vault rules\nbe terse" {
			t.Fatalf("system = %q", body.System)
		}
		if len(body.Messages) != 3 {
			t.Fatalf("messages = %#v", body.Messages)
		}
		if body.Messages[0].Role != "user" || len(body.Messages[0].Content) != 1 ||
			body.Messages[0].Content[0].Type != "text" || body.Messages[0].Content[0].Text != "hi" {
			t.Fatalf("messages[0] = %#v", body.Messages[0])
		}
		call := body.Messages[1]
		if call.Role != "assistant" || len(call.Content) != 1 || call.Content[0].Type != "tool_use" ||
			call.Content[0].ID != "toolu_1" || call.Content[0].Name != "list_folders" ||
			string(call.Content[0].Input) != `{"path":"books"}` {
			t.Fatalf("messages[1] = %#v", call)
		}
		// A tool result is not its own role in the Messages API: it rides back as
		// a user turn carrying a tool_result block.
		result := body.Messages[2]
		if result.Role != "user" || len(result.Content) != 1 || result.Content[0].Type != "tool_result" ||
			result.Content[0].ToolUseID != "toolu_1" || result.Content[0].Content != "books/reading" {
			t.Fatalf("messages[2] = %#v", result)
		}
		response := `{"stop_reason":"tool_use","content":[{"type":"text","text":"looking"},{"type":"tool_use","id":"toolu_2","name":"list_notes","input":{"folder":"books"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
	})}

	out, err := provider.ChatWithToolsResult(t.Context(), []models.Message{
		{Role: "system", Content: "vault rules"},
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []models.ToolCall{{ID: "toolu_1", Type: "function", Function: models.ToolCallFunction{Name: "list_folders", Arguments: json.RawMessage(`{"path":"books"}`)}}}},
		{Role: "tool", ToolCallID: "toolu_1", Content: "books/reading"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Message.Role != "assistant" || out.Message.Content != "looking" || out.DoneReason != "tool_use" {
		t.Fatalf("result = %+v", out)
	}
	if len(out.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", out.Message.ToolCalls)
	}
	got := out.Message.ToolCalls[0]
	if got.ID != "toolu_2" || got.Type != "function" || got.Function.Name != "list_notes" || string(got.Function.Arguments) != `{"folder":"books"}` {
		t.Fatalf("tool call = %+v", got)
	}
}

func TestAnthropicProviderSendsToolsAsInputSchema(t *testing.T) {
	provider := NewAnthropicProvider("anthropic", "https://api.anthropic.test/v1", "ATHENA_TEST_ANTHROPIC_KEY", "claude-haiku-4-5")
	t.Setenv("ATHENA_TEST_ANTHROPIC_KEY", "env-key")
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("x-api-key") != "env-key" {
			t.Fatalf("x-api-key = %q", request.Header.Get("x-api-key"))
		}
		var body struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"input_schema"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Name != "list_folders" || body.Tools[0].Description != "list vault folders" {
			t.Fatalf("tools = %#v", body.Tools)
		}
		// A 2B local model chokes on a schema with no "properties"; the shared
		// normalizer fills it in before the adapter renames it to input_schema.
		if body.Tools[0].InputSchema["type"] != "object" {
			t.Fatalf("input_schema = %#v", body.Tools[0].InputSchema)
		}
		if properties, ok := body.Tools[0].InputSchema["properties"].(map[string]any); !ok || properties == nil {
			t.Fatalf("input_schema properties = %#v", body.Tools[0].InputSchema["properties"])
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"stop_reason":"end_turn","content":[]}`))}, nil
	})}

	if _, err := provider.ChatWithToolsResult(t.Context(), []models.Message{{Role: "user", Content: "folders?"}}, []models.ToolDefinition{{
		Type: "function", Function: models.ToolFunction{Name: "list_folders", Description: "list vault folders"},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestAnthropicProviderReportsMissingAPIKey(t *testing.T) {
	provider := NewAnthropicProvider("anthropic", "https://api.anthropic.test/v1", "ATHENA_TEST_ANTHROPIC_KEY", "claude-sonnet-4-5")
	t.Setenv("ATHENA_TEST_ANTHROPIC_KEY", "")
	provider.http = &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("provider called the API without a key")
		return nil, nil
	})}

	_, err := provider.ChatWithToolsResult(t.Context(), []models.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "ATHENA_TEST_ANTHROPIC_KEY") {
		t.Fatalf("error = %v, want one naming the environment variable", err)
	}
}

func TestAnthropicProviderReportsStatusError(t *testing.T) {
	provider := NewAnthropicProvider("anthropic", "https://api.anthropic.test/v1", "ATHENA_TEST_ANTHROPIC_KEY", "claude-sonnet-4-5")
	t.Setenv("ATHENA_TEST_ANTHROPIC_KEY", "key")
	provider.http = &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`))}, nil
	})}

	_, err := provider.ChatWithToolsResult(t.Context(), []models.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected an error for a non-200 status")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want the status and the response body", err)
	}
}

func TestAnthropicProviderStreamsWholeAnswerToCallback(t *testing.T) {
	provider := NewAnthropicProvider("anthropic", "https://api.anthropic.test/v1", "ATHENA_TEST_ANTHROPIC_KEY", "claude-sonnet-4-5")
	t.Setenv("ATHENA_TEST_ANTHROPIC_KEY", "key")
	provider.http = &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"stop_reason":"end_turn","content":[{"type":"text","text":"one "},{"type":"text","text":"two"}]}`))}, nil
	})}

	var chunks []string
	// The adapter does not stream: it emits the finished answer as a single
	// token so callers built for streaming still render it.
	content, err := provider.StreamChatWith(t.Context(), []models.Message{{Role: "user", Content: "count"}}, StreamCallbacks{OnToken: func(delta string) {
		chunks = append(chunks, delta)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if content != "one two" || strings.Join(chunks, "|") != "one two" {
		t.Fatalf("content=%q chunks=%q", content, chunks)
	}
}
