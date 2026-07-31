package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tiredbooy/internal/models"
)

type Client struct {
	host       string
	chatModel  string
	embedModel string
	http       *http.Client

	mu sync.RWMutex
}

func NewClient(host, chatModel, embedModel string) *Client {
	return &Client{
		host:       host,
		chatModel:  chatModel,
		embedModel: embedModel,
		http:       &http.Client{},
	}
}

func (c *Client) ChatModel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.chatModel
}

func (c *Client) SetChatModel(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chatModel = strings.TrimSpace(name)
}

func (c *Client) EmbedModel() string {
	return c.embedModel
}

func (c *Client) EnsureRunning(ctx context.Context) error {
	if c.isUp(ctx) {
		return nil
	}

	fmt.Println("Ollama isn't running — starting it...")

	cmd := exec.Command("ollama", "serve")
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start Ollama: %w", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if c.isUp(ctx) {
			fmt.Println("Ollama is Up.")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("Ollama did not become reachable within 30s")
}

func (c *Client) isUp(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ModelInfo is a locally available Ollama model.
type ModelInfo struct {
	Name          string
	Size          int64
	Capabilities  []string
	ParameterSize string
}

// ListModels returns every model Ollama has pulled.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models: status %d", resp.StatusCode)
	}

	var raw struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Details struct {
				ParameterSize string `json:"parameter_size"`
			} `json:"details"`
			// Newer Ollama builds put capabilities on the model object
			// via /api/show; tags may omit them. We best-effort read both.
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	out := make([]ModelInfo, 0, len(raw.Models))
	for _, m := range raw.Models {
		out = append(out, ModelInfo{
			Name:          m.Name,
			Size:          m.Size,
			Capabilities:  m.Capabilities,
			ParameterSize: m.Details.ParameterSize,
		})
	}
	return out, nil
}

// ChatModels filters ListModels to ones that look usable for chat
// (excludes known embedding-only names when capabilities are absent).
func (c *Client) ChatModels(ctx context.Context) ([]ModelInfo, error) {
	all, err := c.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	var out []ModelInfo
	for _, m := range all {
		if isEmbeddingOnly(m) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func isEmbeddingOnly(m ModelInfo) bool {
	name := strings.ToLower(m.Name)
	if strings.Contains(name, "embed") {
		return true
	}
	if len(m.Capabilities) == 0 {
		return false
	}
	hasCompletion := false
	for _, cap := range m.Capabilities {
		switch strings.ToLower(cap) {
		case "embedding":
			// keep scanning — some models advertise both
		case "completion", "tools", "thinking", "vision", "insert":
			hasCompletion = true
		}
	}
	// If capabilities are present and none look chat-like, treat as embed-only.
	if !hasCompletion {
		for _, cap := range m.Capabilities {
			if strings.EqualFold(cap, "embedding") {
				return true
			}
		}
	}
	return !hasCompletion && len(m.Capabilities) > 0
}

type chatResponse struct {
	Message struct {
		Role     string `json:"role"`
		Content  string `json:"content"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"message"`
	Done bool `json:"done"`
}

// StreamCallbacks let the UI show progress before the first visible token.
type StreamCallbacks struct {
	// OnThinking is called for reasoning/thinking deltas (may be empty).
	OnThinking func(delta string)
	// OnToken is called for visible reply tokens.
	OnToken func(delta string)
}

func (c *Client) StreamChat(ctx context.Context, messages []models.Message, onToken func(string)) (string, error) {
	return c.StreamChatWith(ctx, messages, StreamCallbacks{OnToken: onToken})
}

func (c *Client) StreamChatWith(ctx context.Context, messages []models.Message, cb StreamCallbacks) (string, error) {
	model := c.ChatModel()

	body, err := json.Marshal(models.MessageReq{
		Model:     model,
		Messages:  messages,
		Stream:    true,
		KeepAlive: "60s",
	})
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("call ollama chat (model %q pulled?): %w", model, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("ollama chat returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var full strings.Builder
	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk chatResponse
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return full.String(), fmt.Errorf("decode stream chunk: %w", err)
		}
		if chunk.Message.Thinking != "" && cb.OnThinking != nil {
			cb.OnThinking(chunk.Message.Thinking)
		}
		if chunk.Message.Content != "" {
			if cb.OnToken != nil {
				cb.OnToken(chunk.Message.Content)
			}
			full.WriteString(chunk.Message.Content)
		}
	}
	return full.String(), nil
}

func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(models.EmbeddingReq{Model: c.embedModel, Prompt: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ollama embeddings (model %q pulled?): %w", c.embedModel, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embeddings returned status %d", resp.StatusCode)
	}

	var out models.EmbeddingResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	return out.Embedding, nil
}
