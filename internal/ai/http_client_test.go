package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/models"
)

func TestProviderRefusesCrossHostRedirectWithCredentials(t *testing.T) {
	var leaked string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization")
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"stolen"}}]}`))
	}))
	defer attacker.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer provider.Close()

	t.Setenv("ATHENA_REDIRECT_TEST_KEY", "secret-key")
	client := NewOpenAICompatibleProvider("Test", provider.URL+"/v1", "ATHENA_REDIRECT_TEST_KEY", "test-model")
	_, err := client.ChatWithToolsResult(context.Background(), []models.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected the cross-host redirect to be refused")
	}
	if !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("error = %v", err)
	}
	if leaked != "" {
		t.Fatalf("credentials followed the redirect: %q", leaked)
	}
}

func TestProviderFollowsSameHostRedirect(t *testing.T) {
	var authorized string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/chat/completions" {
			http.Redirect(w, r, "/v2/chat/completions", http.StatusTemporaryRedirect)
			return
		}
		authorized = r.Header.Get("Authorization")
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	t.Setenv("ATHENA_REDIRECT_TEST_KEY", "secret-key")
	client := NewOpenAICompatibleProvider("Test", server.URL+"/v1", "ATHENA_REDIRECT_TEST_KEY", "test-model")
	result, err := client.ChatWithToolsResult(context.Background(), []models.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Content != "ok" || authorized != "Bearer secret-key" {
		t.Fatalf("content = %q, authorization = %q", result.Message.Content, authorized)
	}
}

func TestProviderHTTPClientHasNoOverallTimeout(t *testing.T) {
	// A chat completion can legitimately run for minutes; only the connection
	// timeouts may bound it.
	client := newProviderHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("chat client timeout = %s, want none", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", client.Transport)
	}
	if transport.TLSHandshakeTimeout == 0 || transport.ResponseHeaderTimeout == 0 {
		t.Fatalf("hung connections are unbounded: %#v", transport)
	}
	if newOAuthHTTPClient().Timeout == 0 {
		t.Fatal("OAuth client must bound the whole exchange")
	}
}
