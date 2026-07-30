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
	"time"

	"github.com/tiredbooy/internal/models"
)

type Client struct {
	host       string
	chatModel  string
	embedModel string
	http       *http.Client
}

func NewClient(host, chatModel, embedModel string) *Client {
	return &Client{
		host:       host,
		chatModel:  chatModel,
		embedModel: embedModel,
		http:       &http.Client{},
	}
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

type chatResponse struct {
	Message models.Message `json:"message"`
}

func (c *Client) StreamChat(ctx context.Context, messages []models.Message, onToken func(string)) (string, error) {
	body, err := json.Marshal(models.MessageReq{
		Model:    c.chatModel,
		Messages: messages,
		Stream:   true,
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
		return "", fmt.Errorf("call ollama chat (model %q pulled?): %w", c.chatModel, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama chat returned status %d", resp.StatusCode)
	}

	// Stream mode sends one JSON object per line (NDJSON), not one big blob.
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
		onToken(chunk.Message.Content)
		full.WriteString(chunk.Message.Content)
	}
	return full.String(), nil
}

// func (c *Client) Chat(ctx context.Context, messages []models.Message) (string, error) {
// 	body, err := json.Marshal(models.MessageReq{
// 		Model:    c.chatModel,
// 		Messages: messages,
// 		Stream:   false,
// 	})
// 	if err != nil {
// 		return "", fmt.Errorf("Marshal chat request: %w", err)
// 	}

// 	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/chat", bytes.NewReader(body))
// 	if err != nil {
// 		return "", fmt.Errorf("Build chat request: %w", err)
// 	}

// 	req.Header.Set("Content-Type", "application/json")

// 	resp, err := c.http.Do(req)
// 	if err != nil {
// 		return "", fmt.Errorf("call ollama chat (model %q pulled?): %w", c.chatModel, err)
// 	}

// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		return "", fmt.Errorf("ollama chat (model %q) returned status code %d", c.chatModel, resp.StatusCode)
// 	}

// 	var out chatResponse
// 	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
// 		return "", fmt.Errorf("Decode chat response: %w", err)
// 	}
// 	return out.Message.Content, nil
// }

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
