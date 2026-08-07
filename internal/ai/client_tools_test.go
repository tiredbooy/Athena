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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
