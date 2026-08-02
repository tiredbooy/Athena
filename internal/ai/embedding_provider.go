package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// EmbeddingProvider is intentionally separate from ChatProvider: vectors are
// storage-compatible only with the model that created them.
type EmbeddingProvider interface {
	Name() string
	EmbedModel() string
	Embed(context.Context, string) ([]float32, error)
}

type OpenAIEmbeddingProvider struct {
	name, baseURL, keyEnv, model string
	http                         *http.Client
}

func NewOpenAIEmbeddingProvider(name, baseURL, keyEnv, model string) *OpenAIEmbeddingProvider {
	return &OpenAIEmbeddingProvider{name: name, baseURL: strings.TrimRight(baseURL, "/"), keyEnv: keyEnv, model: model, http: &http.Client{}}
}
func (p *OpenAIEmbeddingProvider) Name() string       { return p.name }
func (p *OpenAIEmbeddingProvider) EmbedModel() string { return p.model }
func (p *OpenAIEmbeddingProvider) Embed(ctx context.Context, input string) ([]float32, error) {
	key := strings.TrimSpace(os.Getenv(p.keyEnv))
	if p.keyEnv != "" && key == "" {
		return nil, fmt.Errorf("embedding provider %q needs environment variable %s", p.name, p.keyEnv)
	}
	body, err := json.Marshal(map[string]any{"model": p.model, "input": input})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call %s embeddings: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s embeddings returned status %d", p.name, resp.StatusCode)
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("%s returned no embedding", p.name)
	}
	return out.Data[0].Embedding, nil
}
