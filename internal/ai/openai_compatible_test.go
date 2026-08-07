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

func TestOpenAICompatibleProviderChatWithTools(t *testing.T) {
	provider := NewOpenAICompatibleProvider("Test", "https://provider.test/v1", "ATHENA_TEST_KEY", "test-model")
	provider.http = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		var request openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || len(request.Tools) != 1 {
			t.Fatalf("unexpected request: %#v", request)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"list_folders","arguments":"{}"}}]}}]}`)), Header: make(http.Header)}, nil
	})}
	t.Setenv("ATHENA_TEST_KEY", "test-key")
	result, err := provider.ChatWithToolsResult(context.Background(), []models.Message{{Role: "user", Content: "hello"}}, []models.ToolDefinition{{Type: "function", Function: models.ToolFunction{Name: "list_folders"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.DoneReason != "tool_calls" || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestOpenAICompatibleProviderPrefersOAuthTokenSource(t *testing.T) {
	provider := NewOpenAICompatibleProvider("xAI OAuth", "https://provider.test/v1", "ATHENA_UNUSED_KEY", "grok")
	provider.SetAPIKey("unused-direct-key")
	provider.SetTokenSource(func(context.Context) (string, error) { return "oauth-token", nil })
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("authorization = %q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"grok"}]}`)), Header: make(http.Header)}, nil
	})}
	models, err := provider.ChatModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != "grok" {
		t.Fatalf("models = %#v", models)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
