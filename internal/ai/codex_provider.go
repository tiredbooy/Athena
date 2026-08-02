package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/tiredbooy/internal/models"
)

const codexEndpoint = "https://chatgpt.com/backend-api/codex/responses"

// Codex OAuth requests are accepted by the ChatGPT Codex backend only when
// they carry the Codex instruction envelope. Athena keeps its vault-specific
// system context as an additional input message below.
const codexInstructions = "You are Codex, based on GPT-5. You are running as a coding agent in the Codex CLI on a user's computer."

type CodexProvider struct {
	auth      *CodexOAuth
	mu        sync.RWMutex
	chatModel string
	http      *http.Client
}

func NewCodexProvider(auth *CodexOAuth, model string) *CodexProvider {
	return &CodexProvider{auth: auth, chatModel: model, http: &http.Client{}}
}
func (p *CodexProvider) Name() string      { return "ChatGPT subscription" }
func (p *CodexProvider) ChatModel() string { p.mu.RLock(); defer p.mu.RUnlock(); return p.chatModel }
func (p *CodexProvider) SetChatModel(model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chatModel = strings.TrimSpace(model)
}
func (p *CodexProvider) ChatModels(context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{Name: "gpt-5.2"}, {Name: "gpt-5.2-codex"}, {Name: "gpt-5.3-codex"}, {Name: "gpt-5.4"}, {Name: "gpt-5.4-mini"}}, nil
}
func (p *CodexProvider) StreamChatWith(ctx context.Context, messages []models.Message, cb StreamCallbacks) (string, error) {
	result, err := p.ChatWithToolsResult(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	if cb.OnToken != nil {
		cb.OnToken(result.Message.Content)
	}
	return result.Message.Content, nil
}
func (p *CodexProvider) ChatWithToolsResult(ctx context.Context, messages []models.Message, definitions []models.ToolDefinition) (ToolChatResult, error) {
	credentials, err := p.auth.Credentials(ctx)
	if err != nil {
		return ToolChatResult{}, err
	}
	input := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == "tool" {
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
			continue
		}
		for _, call := range message.ToolCalls {
			input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": string(call.Function.Arguments)})
		}
		if message.Content != "" {
			input = append(input, map[string]any{"role": message.Role, "content": []map[string]string{{"type": "input_text", "text": message.Content}}})
		}
	}
	reqBody := map[string]any{"model": p.ChatModel(), "input": input, "instructions": codexInstructions, "store": false}
	if len(definitions) > 0 {
		tools := make([]map[string]any, 0, len(definitions))
		for _, tool := range definitions {
			tools = append(tools, map[string]any{"type": "function", "name": tool.Function.Name, "description": tool.Function.Description, "parameters": tool.Function.Parameters})
		}
		reqBody["tools"] = tools
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return ToolChatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexEndpoint, bytes.NewReader(body))
	if err != nil {
		return ToolChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	if credentials.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", credentials.AccountID)
	}
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("Version", "0.87.0")
	resp, err := p.http.Do(req)
	if err != nil {
		return ToolChatResult{}, fmt.Errorf("call ChatGPT subscription: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ToolChatResult{}, providerStatusError("ChatGPT subscription", "chat", resp)
	}
	var response struct {
		Status string `json:"status"`
		Output []struct {
			Type      string `json:"type"`
			ID        string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return ToolChatResult{}, fmt.Errorf("decode ChatGPT subscription response: %w", err)
	}
	message := models.Message{Role: "assistant"}
	for _, output := range response.Output {
		if output.Type == "function_call" {
			message.ToolCalls = append(message.ToolCalls, models.ToolCall{ID: output.ID, Type: "function", Function: models.ToolCallFunction{Name: output.Name, Arguments: json.RawMessage(output.Arguments)}})
		}
		for _, content := range output.Content {
			message.Content += content.Text
		}
	}
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return ToolChatResult{}, fmt.Errorf("ChatGPT subscription returned no answer")
	}
	return ToolChatResult{Message: message, DoneReason: response.Status}, nil
}
