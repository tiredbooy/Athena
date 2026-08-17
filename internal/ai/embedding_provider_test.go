package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIEmbeddingProviderRequestsAndDecodesVector(t *testing.T) {
	provider := NewOpenAIEmbeddingProvider("openai", "https://api.openai.test/v1/", "ATHENA_TEST_EMBED_KEY", "text-embedding-3-small")
	t.Setenv("ATHENA_TEST_EMBED_KEY", "embed-key")
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != "https://api.openai.test/v1/embeddings" {
			t.Fatalf("request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer embed-key" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		var body struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "text-embedding-3-small" || body.Input != "note body" {
			t.Fatalf("body = %+v", body)
		}
		response := `{"data":[{"embedding":[0.5,-0.25,0]}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
	})}

	vector, err := provider.Embed(t.Context(), "note body")
	if err != nil {
		t.Fatal(err)
	}
	if len(vector) != 3 || vector[0] != 0.5 || vector[1] != -0.25 || vector[2] != 0 {
		t.Fatalf("vector = %v", vector)
	}
	if provider.Name() != "openai" || provider.EmbedModel() != "text-embedding-3-small" {
		t.Fatalf("name=%q model=%q", provider.Name(), provider.EmbedModel())
	}
}

func TestOpenAIEmbeddingProviderOmitsAuthorizationWhenNoKeyEnv(t *testing.T) {
	// A local embedder (Ollama and friends) has no key env; it must not be
	// blocked by the key check and must not send an empty bearer token.
	provider := NewOpenAIEmbeddingProvider("local", "http://127.0.0.1:11434/v1", "", "nomic-embed-text")
	provider.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if _, ok := request.Header["Authorization"]; ok {
			t.Fatalf("authorization header = %q, want none", request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"embedding":[1]}]}`))}, nil
	})}

	if _, err := provider.Embed(t.Context(), "note body"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIEmbeddingProviderReportsMissingAPIKey(t *testing.T) {
	provider := NewOpenAIEmbeddingProvider("openai", "https://api.openai.test/v1", "ATHENA_TEST_EMBED_KEY", "text-embedding-3-small")
	t.Setenv("ATHENA_TEST_EMBED_KEY", "")
	provider.http = &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("provider called the API without a key")
		return nil, nil
	})}

	_, err := provider.Embed(t.Context(), "note body")
	if err == nil || !strings.Contains(err.Error(), "ATHENA_TEST_EMBED_KEY") {
		t.Fatalf("error = %v, want one naming the environment variable", err)
	}
}

func TestOpenAIEmbeddingProviderReportsStatusError(t *testing.T) {
	provider := NewOpenAIEmbeddingProvider("openai", "https://api.openai.test/v1", "ATHENA_TEST_EMBED_KEY", "text-embedding-3-small")
	t.Setenv("ATHENA_TEST_EMBED_KEY", "embed-key")
	provider.http = &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"down"}`))}, nil
	})}

	_, err := provider.Embed(t.Context(), "note body")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want one naming the status", err)
	}
}

func TestOpenAIEmbeddingProviderRejectsEmptyVector(t *testing.T) {
	provider := NewOpenAIEmbeddingProvider("openai", "https://api.openai.test/v1", "ATHENA_TEST_EMBED_KEY", "text-embedding-3-small")
	t.Setenv("ATHENA_TEST_EMBED_KEY", "embed-key")
	provider.http = &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		// A 200 with no vector must not become a zero-length embedding: that
		// would be stored and silently poison every later similarity search.
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"embedding":[]}]}`))}, nil
	})}

	if _, err := provider.Embed(t.Context(), "note body"); err == nil || !strings.Contains(err.Error(), "no embedding") {
		t.Fatalf("error = %v, want a no-embedding error", err)
	}
}
