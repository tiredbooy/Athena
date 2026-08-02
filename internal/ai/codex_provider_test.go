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
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["instructions"] != codexInstructions {
			t.Fatalf("instructions = %#v", body["instructions"])
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","content":[{"text":"hello"}]}]}`))}, nil
	})}
	result, err := provider.ChatWithToolsResult(context.Background(), []models.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Content != "hello" {
		t.Fatalf("content = %q", result.Message.Content)
	}
}
