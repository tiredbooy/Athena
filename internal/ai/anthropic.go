package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/tiredbooy/internal/models"
)

// AnthropicProvider speaks the native Messages API. Anthropic is not
// OpenAI-compatible, so keeping this adapter separate avoids hiding protocol
// differences in the generic custom-provider implementation.
type AnthropicProvider struct {
	name, baseURL, keyEnv string
	mu                    sync.RWMutex
	model                 string
	http                  *http.Client
}

func NewAnthropicProvider(name, baseURL, keyEnv, model string) *AnthropicProvider {
	return &AnthropicProvider{name: name, baseURL: strings.TrimRight(baseURL, "/"), keyEnv: keyEnv, model: model, http: &http.Client{}}
}
func (p *AnthropicProvider) Name() string      { return p.name }
func (p *AnthropicProvider) ChatModel() string { p.mu.RLock(); defer p.mu.RUnlock(); return p.model }
func (p *AnthropicProvider) SetChatModel(model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.model = strings.TrimSpace(model)
}
func (p *AnthropicProvider) ChatModels(context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{Name: "claude-sonnet-4-5"}, {Name: "claude-opus-4-5"}, {Name: "claude-haiku-4-5"}}, nil
}
func (p *AnthropicProvider) StreamChatWith(ctx context.Context, messages []models.Message, cb StreamCallbacks) (string, error) {
	result, err := p.ChatWithToolsResult(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	if cb.OnToken != nil {
		cb.OnToken(result.Message.Content)
	}
	return result.Message.Content, nil
}
func (p *AnthropicProvider) ChatWithToolsResult(ctx context.Context, messages []models.Message, definitions []models.ToolDefinition) (ToolChatResult, error) {
	key := strings.TrimSpace(os.Getenv(p.keyEnv))
	if key == "" {
		return ToolChatResult{}, fmt.Errorf("provider %q needs environment variable %s", p.name, p.keyEnv)
	}
	request := map[string]any{"model": p.ChatModel(), "max_tokens": 4096}
	var system string
	var chatMessages []map[string]any
	for _, message := range messages {
		if message.Role == "system" {
			system += message.Content + "\n"
			continue
		}
		if message.Role == "tool" {
			chatMessages = append(chatMessages, map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": message.Content}}})
			continue
		}
		content := []map[string]any{}
		if message.Content != "" {
			content = append(content, map[string]any{"type": "text", "text": message.Content})
		}
		for _, call := range message.ToolCalls {
			content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": json.RawMessage(call.Function.Arguments)})
		}
		chatMessages = append(chatMessages, map[string]any{"role": message.Role, "content": content})
	}
	if strings.TrimSpace(system) != "" {
		request["system"] = strings.TrimSpace(system)
	}
	request["messages"] = chatMessages
	if len(definitions) > 0 {
		tools := make([]map[string]any, 0, len(definitions))
		for _, tool := range definitions {
			tools = append(tools, map[string]any{"name": tool.Function.Name, "description": tool.Function.Description, "input_schema": tool.Function.Parameters})
		}
		request["tools"] = tools
	}
	body, err := json.Marshal(request)
	if err != nil {
		return ToolChatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return ToolChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := p.http.Do(req)
	if err != nil {
		return ToolChatResult{}, fmt.Errorf("call %s: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ToolChatResult{}, providerStatusError(p.name, "chat", resp)
	}
	var response struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return ToolChatResult{}, fmt.Errorf("decode %s response: %w", p.name, err)
	}
	out := models.Message{Role: "assistant"}
	for _, block := range response.Content {
		if block.Type == "text" {
			out.Content += block.Text
		}
		if block.Type == "tool_use" {
			out.ToolCalls = append(out.ToolCalls, models.ToolCall{ID: block.ID, Type: "function", Function: models.ToolCallFunction{Name: block.Name, Arguments: block.Input}})
		}
	}
	return ToolChatResult{Message: out, DoneReason: response.StopReason}, nil
}
