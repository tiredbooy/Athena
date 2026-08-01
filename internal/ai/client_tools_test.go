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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
