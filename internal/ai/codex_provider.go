package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/tiredbooy/internal/models"
)

const (
	codexEndpoint       = "https://chatgpt.com/backend-api/codex/responses"
	codexModelsEndpoint = "https://chatgpt.com/backend-api/codex/models"
	// The models endpoint uses this compatibility version to decide which
	// server-managed catalog schema and models the client can consume.
	codexClientVersion = "0.147.0"
)

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
func (p *CodexProvider) ChatModels(ctx context.Context) ([]ModelInfo, error) {
	credentials, err := p.auth.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexModelsEndpoint+"?client_version="+codexClientVersion, nil)
	if err != nil {
		return nil, fmt.Errorf("build ChatGPT subscription model request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	authorizeCodexRequest(req, credentials)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list ChatGPT subscription models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, providerStatusError("ChatGPT subscription", "list models", resp)
	}
	var payload struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode ChatGPT subscription models: %w", err)
	}
	available := make([]ModelInfo, 0, len(payload.Models))
	for _, model := range payload.Models {
		name := strings.TrimSpace(model.Slug)
		if name == "" || (model.Visibility != "" && model.Visibility != "list") {
			continue
		}
		available = append(available, ModelInfo{Name: name})
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("ChatGPT subscription returned no available models")
	}
	return available, nil
}
func (p *CodexProvider) StreamChatWith(ctx context.Context, messages []models.Message, cb StreamCallbacks) (string, error) {
	result, err := p.chatWithToolsResult(ctx, messages, nil, false, cb.OnToken)
	if err != nil {
		return "", err
	}
	return result.Message.Content, nil
}
func (p *CodexProvider) ChatWithToolsResult(ctx context.Context, messages []models.Message, definitions []models.ToolDefinition) (ToolChatResult, error) {
	return p.chatWithToolsResult(ctx, messages, definitions, false, nil)
}

func (p *CodexProvider) ChatWithRequiredToolsResult(ctx context.Context, messages []models.Message, definitions []models.ToolDefinition) (ToolChatResult, error) {
	if len(definitions) == 0 {
		return ToolChatResult{}, fmt.Errorf("required tool selection needs at least one tool definition")
	}
	return p.chatWithToolsResult(ctx, messages, definitions, true, nil)
}

func (p *CodexProvider) chatWithToolsResult(ctx context.Context, messages []models.Message, definitions []models.ToolDefinition, requireTool bool, onToken func(string)) (ToolChatResult, error) {
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
			input = append(input, map[string]any{"role": message.Role, "content": []map[string]string{{"type": codexContentType(message.Role), "text": message.Content}}})
		}
	}
	// The ChatGPT subscription Codex endpoint is streaming-only. Unlike the
	// public Responses API default, omitting this field is rejected with 400.
	reqBody := map[string]any{"model": p.ChatModel(), "input": input, "instructions": codexInstructions, "store": false, "stream": true}
	if len(definitions) > 0 {
		tools := make([]map[string]any, 0, len(definitions))
		for _, tool := range normalizedToolDefinitions(definitions) {
			tools = append(tools, map[string]any{"type": "function", "name": tool.Function.Name, "description": tool.Function.Description, "parameters": tool.Function.Parameters})
		}
		reqBody["tools"] = tools
	}
	if requireTool {
		// Required tool selection prevents a mutation-planning turn from
		// degrading into a prose promise. One call at a time keeps the read and
		// decision protocol deterministic for Athena's bounded loop.
		reqBody["tool_choice"] = "required"
		reqBody["parallel_tool_calls"] = false
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
	req.Header.Set("Accept", "text/event-stream")
	authorizeCodexRequest(req, credentials)
	resp, err := p.http.Do(req)
	if err != nil {
		return ToolChatResult{}, fmt.Errorf("call ChatGPT subscription: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ToolChatResult{}, providerStatusError("ChatGPT subscription", "chat", resp)
	}
	message, status, err := decodeCodexStream(resp.Body, onToken)
	if err != nil {
		return ToolChatResult{}, fmt.Errorf("decode ChatGPT subscription stream: %w", err)
	}
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return ToolChatResult{}, fmt.Errorf("ChatGPT subscription returned no answer")
	}
	return ToolChatResult{Message: message, DoneReason: status}, nil
}

func codexContentType(role string) string {
	if role == "assistant" {
		return "output_text"
	}
	return "input_text"
}

func authorizeCodexRequest(req *http.Request, credentials CodexCredentials) {
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	if credentials.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", credentials.AccountID)
	}
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("Version", codexClientVersion)
}

type codexOutput struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Text string `json:"text"`
	} `json:"content"`
}

type codexStreamEvent struct {
	Type    string      `json:"type"`
	Delta   string      `json:"delta"`
	Message string      `json:"message"`
	Item    codexOutput `json:"item"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
	Response *struct {
		Status string        `json:"status"`
		Output []codexOutput `json:"output"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

func decodeCodexStream(body io.Reader, onToken func(string)) (models.Message, string, error) {
	message := models.Message{Role: "assistant"}
	status := ""
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	eventName := ""
	dataLines := make([]string, 0, 1)
	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if data == "[DONE]" {
			eventName = ""
			return nil
		}
		var event codexStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode %s event: %w", eventName, err)
		}
		if event.Type == "" {
			event.Type = eventName
		}
		eventName = ""
		switch event.Type {
		case "response.output_text.delta":
			message.Content += event.Delta
			if onToken != nil && event.Delta != "" {
				onToken(event.Delta)
			}
		case "response.output_item.done":
			appendCodexOutput(&message, event.Item, false)
		case "response.completed":
			if event.Response != nil {
				status = event.Response.Status
				for _, output := range event.Response.Output {
					appendCodexOutput(&message, output, message.Content == "")
				}
			}
		case "response.failed", "response.incomplete":
			if event.Response != nil && event.Response.Error != nil && event.Response.Error.Message != "" {
				return fmt.Errorf("%s", event.Response.Error.Message)
			}
			return fmt.Errorf("ChatGPT response %s", strings.TrimPrefix(event.Type, "response."))
		case "error":
			if event.Message != "" {
				return fmt.Errorf("%s", event.Message)
			}
			if event.Error != nil && event.Error.Message != "" {
				return fmt.Errorf("%s", event.Error.Message)
			}
			return fmt.Errorf("ChatGPT stream returned an error")
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return message, status, err
			}
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return message, status, err
	}
	if err := flush(); err != nil {
		return message, status, err
	}
	return message, status, nil
}

func appendCodexOutput(message *models.Message, output codexOutput, includeText bool) {
	if output.Type == "function_call" {
		callID := output.CallID
		if callID == "" {
			callID = output.ID
		}
		for _, existing := range message.ToolCalls {
			if existing.ID == callID {
				return
			}
		}
		message.ToolCalls = append(message.ToolCalls, models.ToolCall{ID: callID, Type: "function", Function: models.ToolCallFunction{Name: output.Name, Arguments: json.RawMessage(output.Arguments)}})
	}
	if includeText {
		for _, content := range output.Content {
			message.Content += content.Text
		}
	}
}
