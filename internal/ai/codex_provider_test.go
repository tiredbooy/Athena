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

func TestCodexProviderUsesCodexEnvelope(t *testing.T) {
	auth := &CodexOAuth{credentials: CodexCredentials{AccessToken: "token", RefreshToken: "refresh", ExpiresAt: 4_102_444_800}, http: &http.Client{}}
	provider := NewCodexProvider(auth, "gpt-5.2")
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Originator") != "codex_cli_rs" {
			t.Fatalf("originator = %q", request.Header.Get("Originator"))
		}
		var body struct {
			Instructions string `json:"instructions"`
			Stream       bool   `json:"stream"`
			Input        []struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
				} `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Instructions != codexInstructions {
			t.Fatalf("instructions = %#v", body.Instructions)
		}
		if !body.Stream {
			t.Fatal("stream = false, want true")
		}
		wantTypes := []string{"input_text", "output_text", "input_text"}
		if len(body.Input) != len(wantTypes) {
			t.Fatalf("input = %#v", body.Input)
		}
		for i, want := range wantTypes {
			if len(body.Input[i].Content) != 1 || body.Input[i].Content[0].Type != want {
				t.Fatalf("input[%d] content = %#v, want %s", i, body.Input[i].Content, want)
			}
		}
		if request.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("accept = %q", request.Header.Get("Accept"))
		}
		stream := "event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hel\"}\n\n" +
			"event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"text\":\"hello\"}]}]}}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}
	result, err := provider.ChatWithToolsResult(context.Background(), []models.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "continue"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Content != "hello" {
		t.Fatalf("content = %q", result.Message.Content)
	}
	if result.DoneReason != "completed" {
		t.Fatalf("done reason = %q", result.DoneReason)
	}
}

func TestCodexProviderLoadsAvailableModelsFromSubscription(t *testing.T) {
	auth := &CodexOAuth{credentials: CodexCredentials{AccessToken: "token", RefreshToken: "refresh", ExpiresAt: 4_102_444_800, AccountID: "account"}, http: &http.Client{}}
	provider := NewCodexProvider(auth, "gpt-5.4")
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/backend-api/codex/models" {
			t.Fatalf("request = %s %s", request.Method, request.URL.String())
		}
		if request.URL.Query().Get("client_version") != codexClientVersion {
			t.Fatalf("client_version = %q", request.URL.Query().Get("client_version"))
		}
		if request.Header.Get("Authorization") != "Bearer token" || request.Header.Get("ChatGPT-Account-Id") != "account" {
			t.Fatalf("subscription headers were not applied")
		}
		body := `{"models":[{"slug":"gpt-current","visibility":"list"},{"slug":"gpt-hidden","visibility":"hide"},{"slug":"gpt-next","visibility":"list"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}

	models, err := provider.ChatModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Name != "gpt-current" || models[1].Name != "gpt-next" {
		t.Fatalf("models = %+v", models)
	}
}

func TestCodexProviderDecodesStreamedToolCall(t *testing.T) {
	auth := &CodexOAuth{credentials: CodexCredentials{AccessToken: "token", RefreshToken: "refresh", ExpiresAt: 4_102_444_800}, http: &http.Client{}}
	provider := NewCodexProvider(auth, "gpt-5.2")
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		var body struct {
			Tools []struct {
				Parameters map[string]any `json:"parameters"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Parameters["type"] != "object" {
			t.Fatalf("tool parameters = %#v", body.Tools)
		}
		if properties, ok := body.Tools[0].Parameters["properties"].(map[string]any); !ok || properties == nil {
			t.Fatalf("tool properties = %#v", body.Tools[0].Parameters["properties"])
		}
		stream := "event: response.output_item.done\n" +
			`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"list_folders","arguments":"{}"}}` + "\n\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"list_folders","arguments":"{}"}]}}` + "\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}

	result, err := provider.ChatWithToolsResult(t.Context(), []models.Message{{Role: "user", Content: "show folders"}}, []models.ToolDefinition{{
		Type: "function", Function: models.ToolFunction{Name: "list_folders"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].ID != "call_1" || result.Message.ToolCalls[0].Function.Name != "list_folders" {
		t.Fatalf("tool calls = %+v", result.Message.ToolCalls)
	}
}

func TestCodexProviderCanRequireOneToolDecision(t *testing.T) {
	auth := &CodexOAuth{credentials: CodexCredentials{AccessToken: "token", RefreshToken: "refresh", ExpiresAt: 4_102_444_800}, http: &http.Client{}}
	provider := NewCodexProvider(auth, "gpt-5.6-luna")
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		var body struct {
			ToolChoice        string `json:"tool_choice"`
			ParallelToolCalls *bool  `json:"parallel_tool_calls"`
			Tools             []any  `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ToolChoice != "required" {
			t.Fatalf("tool_choice = %q, want required", body.ToolChoice)
		}
		if body.ParallelToolCalls == nil || *body.ParallelToolCalls {
			t.Fatalf("parallel_tool_calls = %v, want false", body.ParallelToolCalls)
		}
		if len(body.Tools) != 1 {
			t.Fatalf("tools = %#v", body.Tools)
		}
		stream := "event: response.output_item.done\n" +
			`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call-1","name":"propose_actions","arguments":"{\"actions\":[{\"type\":\"ensure_folders\",\"paths\":[\"books/reading/science-fiction\"]}]}"}}` + "\n\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","response":{"status":"completed"}}` + "\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}

	result, err := provider.ChatWithRequiredToolsResult(t.Context(), []models.Message{{Role: "user", Content: "organize my books"}}, []models.ToolDefinition{{
		Type: "function", Function: models.ToolFunction{Name: "propose_actions", Parameters: map[string]any{"type": "object"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].Function.Name != "propose_actions" {
		t.Fatalf("tool calls = %+v", result.Message.ToolCalls)
	}
}

func TestCodexProviderStreamsTextCallbacks(t *testing.T) {
	auth := &CodexOAuth{credentials: CodexCredentials{AccessToken: "token", RefreshToken: "refresh", ExpiresAt: 4_102_444_800}, http: &http.Client{}}
	provider := NewCodexProvider(auth, "gpt-5.2")
	provider.http = &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"one\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\" two\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}
	var chunks []string
	content, err := provider.StreamChatWith(t.Context(), []models.Message{{Role: "user", Content: "count"}}, StreamCallbacks{OnToken: func(delta string) {
		chunks = append(chunks, delta)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if content != "one two" || strings.Join(chunks, "|") != "one| two" {
		t.Fatalf("content=%q chunks=%q", content, chunks)
	}
}

func TestDecodeCodexStreamReturnsProviderError(t *testing.T) {
	stream := "event: error\n" +
		`data: {"type":"error","message":"subscription model is unavailable"}` + "\n\n"
	_, _, err := decodeCodexStream(strings.NewReader(stream), nil)
	if err == nil || !strings.Contains(err.Error(), "subscription model is unavailable") {
		t.Fatalf("error = %v", err)
	}
}
