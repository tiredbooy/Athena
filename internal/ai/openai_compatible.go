package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/tiredbooy/internal/models"
)

// OpenAICompatibleProvider supports OpenAI's Chat Completions wire format,
// which is also implemented by many self-hosted servers. It deliberately does
// not claim that every compatible server supports tools or model listing.
type OpenAICompatibleProvider struct {
	name, baseURL, apiKeyEnv string
	mu                       sync.RWMutex
	chatModel, apiKey        string
	tokenSource              func(context.Context) (string, error)
	http                     *http.Client
}

func NewOpenAICompatibleProvider(name, baseURL, apiKeyEnv, chatModel string) *OpenAICompatibleProvider {
	return &OpenAICompatibleProvider{
		name: strings.TrimSpace(name), baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKeyEnv: strings.TrimSpace(apiKeyEnv), chatModel: strings.TrimSpace(chatModel), http: &http.Client{},
	}
}

func (p *OpenAICompatibleProvider) Name() string { return p.name }
func (p *OpenAICompatibleProvider) ChatModel() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.chatModel
}
func (p *OpenAICompatibleProvider) SetChatModel(model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chatModel = strings.TrimSpace(model)
}

func (p *OpenAICompatibleProvider) SetAPIKey(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.apiKey = strings.TrimSpace(key)
}

// SetTokenSource lets OAuth-backed OpenAI-compatible providers supply a fresh
// bearer token for every request without coupling this wire-format adapter to
// a particular provider's authorization protocol.
func (p *OpenAICompatibleProvider) SetTokenSource(source func(context.Context) (string, error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokenSource = source
}

func (p *OpenAICompatibleProvider) authorize(req *http.Request) error {
	p.mu.RLock()
	key := p.apiKey
	tokenSource := p.tokenSource
	p.mu.RUnlock()
	if tokenSource != nil {
		token, err := tokenSource(req.Context())
		if err != nil {
			return err
		}
		if strings.TrimSpace(token) == "" {
			return fmt.Errorf("provider %q returned an empty OAuth access token", p.name)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
		return nil
	}
	if p.apiKeyEnv == "" {
		return nil
	}
	key = strings.TrimSpace(os.Getenv(p.apiKeyEnv))
	if key == "" {
		return fmt.Errorf("provider %q needs environment variable %s", p.name, p.apiKeyEnv)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	return nil
}

func (p *OpenAICompatibleProvider) ChatModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("build %s model request: %w", p.name, err)
	}
	if err := p.authorize(req); err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list %s models: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, providerStatusError(p.name, "list models", resp)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s models: %w", p.name, err)
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, model := range payload.Data {
		if model.ID != "" {
			models = append(models, ModelInfo{Name: model.ID})
		}
	}
	return models, nil
}

func (p *OpenAICompatibleProvider) ChatWithToolsResult(ctx context.Context, messages []models.Message, definitions []models.ToolDefinition) (ToolChatResult, error) {
	request := openAIChatRequest{Model: p.ChatModel(), Messages: make([]openAIMessage, 0, len(messages))}
	for _, message := range messages {
		request.Messages = append(request.Messages, openAIMessageFrom(message))
	}
	for _, definition := range definitions {
		request.Tools = append(request.Tools, openAITool{Type: definition.Type, Function: definition.Function})
	}
	body, err := json.Marshal(request)
	if err != nil {
		return ToolChatResult{}, fmt.Errorf("marshal %s chat request: %w", p.name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ToolChatResult{}, fmt.Errorf("build %s chat request: %w", p.name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := p.authorize(req); err != nil {
		return ToolChatResult{}, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return ToolChatResult{}, fmt.Errorf("call %s chat: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ToolChatResult{}, providerStatusError(p.name, "chat", resp)
	}
	var result struct {
		Choices []struct {
			FinishReason string        `json:"finish_reason"`
			Message      openAIMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ToolChatResult{}, fmt.Errorf("decode %s chat response: %w", p.name, err)
	}
	if len(result.Choices) == 0 {
		return ToolChatResult{}, fmt.Errorf("%s returned no choices", p.name)
	}
	choice := result.Choices[0]
	message := models.Message{Role: choice.Message.Role, Content: choice.Message.Content}
	for _, call := range choice.Message.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, models.ToolCall{ID: call.ID, Type: call.Type, Function: models.ToolCallFunction{Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)}})
	}
	return ToolChatResult{Message: message, DoneReason: choice.FinishReason}, nil
}

// StreamChatWith keeps the legacy terminal loop compatible. The Bubble UI
// uses ChatWithToolsResult, so this emits the finished response once.
func (p *OpenAICompatibleProvider) StreamChatWith(ctx context.Context, messages []models.Message, cb StreamCallbacks) (string, error) {
	result, err := p.ChatWithToolsResult(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Message.Content) == "" {
		return "", fmt.Errorf("provider %q produced no visible response", p.name)
	}
	if cb.OnToken != nil {
		cb.OnToken(result.Message.Content)
	}
	return result.Message.Content, nil
}

type openAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
}
type openAITool struct {
	Type     string              `json:"type"`
	Function models.ToolFunction `json:"function"`
}
type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}
type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}
type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func openAIMessageFrom(message models.Message) openAIMessage {
	out := openAIMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID}
	for _, call := range message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, openAIToolCall{ID: call.ID, Type: call.Type, Function: openAIFunctionCall{Name: call.Function.Name, Arguments: string(call.Function.Arguments)}})
	}
	return out
}

func providerStatusError(provider, operation string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s %s returned status %d: %s", provider, operation, resp.StatusCode, strings.TrimSpace(string(body)))
}
