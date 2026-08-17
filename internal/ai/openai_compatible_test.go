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
		parameters := request.Tools[0].Function.Parameters
		if parameters["type"] != "object" {
			t.Fatalf("parameters = %#v", parameters)
		}
		if properties, ok := parameters["properties"].(map[string]any); !ok || properties == nil {
			t.Fatalf("properties = %#v", parameters["properties"])
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

func TestOpenAICompatibleProviderRequiresToolChoiceOnlyOnMutationPath(t *testing.T) {
	provider := NewOpenAICompatibleProvider("Test", "https://provider.test/v1", "", "test-model")
	var sent []string
	provider.http = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		sent = append(sent, decodedField(t, r, "tool_choice"))
		return toolCallResponse(), nil
	})}
	tools := []models.ToolDefinition{{Type: "function", Function: models.ToolFunction{Name: "create_note"}}}
	if _, err := provider.ChatWithRequiredToolsResult(context.Background(), []models.Message{{Role: "user", Content: "make a note"}}, tools); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ChatWithToolsResult(context.Background(), []models.Message{{Role: "user", Content: "read a note"}}, tools); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != `"required"` || sent[1] != "" {
		t.Fatalf("tool_choice per request = %#v, want required then absent", sent)
	}
}

func TestOpenAICompatibleProviderRequiredToolsSurviveRejectedToolChoice(t *testing.T) {
	provider := NewOpenAICompatibleProvider("Test", "https://provider.test/v1", "", "test-model")
	var sent []string
	provider.http = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		choice := decodedField(t, r, "tool_choice")
		sent = append(sent, choice)
		if choice != "" {
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unknown field: tool_choice"}}`)), Header: make(http.Header)}, nil
		}
		return toolCallResponse(), nil
	})}
	tools := []models.ToolDefinition{{Type: "function", Function: models.ToolFunction{Name: "create_note"}}}
	result, err := provider.ChatWithRequiredToolsResult(context.Background(), []models.Message{{Role: "user", Content: "make a note"}}, tools)
	if err != nil {
		t.Fatalf("rejected tool_choice must not kill the turn: %v", err)
	}
	if len(result.Message.ToolCalls) != 1 {
		t.Fatalf("result = %#v", result)
	}
	// The rejection is remembered, so the next mutation turn does not pay a
	// failed request to rediscover it.
	if _, err := provider.ChatWithRequiredToolsResult(context.Background(), []models.Message{{Role: "user", Content: "make another"}}, tools); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 3 || sent[0] != `"required"` || sent[1] != "" || sent[2] != "" {
		t.Fatalf("tool_choice per request = %#v, want one attempt then none", sent)
	}
}

// decodedField returns the raw JSON of one request field, or "" when the field
// was omitted, so a test can tell "absent" from "empty value".
func decodedField(t *testing.T, r *http.Request, field string) string {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return string(body[field])
}

func toolCallResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"create_note","arguments":"{}"}}]}}]}`)), Header: make(http.Header)}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
