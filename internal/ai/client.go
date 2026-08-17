package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
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

	mu          sync.RWMutex
	inferenceMu sync.Mutex
	toolSupport map[string]NativeToolSupport
	contextSize map[string]int
}

func NewClient(host, chatModel, embedModel string) *Client {
	return &Client{
		host:        host,
		chatModel:   chatModel,
		embedModel:  embedModel,
		http:        newProviderHTTPClient(),
		toolSupport: make(map[string]NativeToolSupport),
		contextSize: make(map[string]int),
	}
}

func (c *Client) ChatModel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.chatModel
}

func (c *Client) Name() string { return "Ollama" }

func (c *Client) SetChatModel(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chatModel = strings.TrimSpace(name)
}

// SetHost is used only when restoring the built-in Ollama connection.
func (c *Client) SetHost(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.host = strings.TrimRight(strings.TrimSpace(host), "/")
}

func (c *Client) EmbedModel() string {
	return c.embedModel
}

func (c *Client) EnsureRunning(ctx context.Context) error {
	if c.isUp(ctx) {
		return nil
	}

	fmt.Fprintln(os.Stderr, "Ollama isn't running — starting it...")

	cmd := exec.Command("ollama", "serve")
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start Ollama: %w", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if c.isUp(ctx) {
			fmt.Fprintln(os.Stderr, "Ollama is up.")
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
	Message    models.Message `json:"message"`
	Done       bool           `json:"done"`
	DoneReason string         `json:"done_reason"`
}

type ToolChatResult struct {
	Message    models.Message
	DoneReason string
}

// ChatWithTools performs one non-streaming model step. The application owns
// the tool loop so it can validate and execute only the capabilities it has
// explicitly exposed.
func (c *Client) ChatWithTools(ctx context.Context, messages []models.Message, tools []models.ToolDefinition) (models.Message, error) {
	result, err := c.ChatWithToolsResult(ctx, messages, tools)
	return result.Message, err
}

// NativeToolSupport inspects the local model manifest once per model. Ollama
// can advertise a tools capability while a custom template still omits
// {{ .Tools }}; sending native tools to that combination wastes one complete
// inference before the application can fall back to plain planning chat.
func (c *Client) NativeToolSupport(ctx context.Context) (NativeToolSupport, error) {
	model := c.ChatModel()
	c.mu.RLock()
	cached, ok := c.toolSupport[model]
	c.mu.RUnlock()
	if ok {
		return cached, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]string{"name": model})
	if err != nil {
		return NativeToolSupport{}, fmt.Errorf("encode model tool support request: %w", err)
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, c.host+"/api/show", bytes.NewReader(body))
	if err != nil {
		return NativeToolSupport{}, fmt.Errorf("build model tool support request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return NativeToolSupport{}, fmt.Errorf("query model tool support: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return NativeToolSupport{}, fmt.Errorf("query model tool support: status %d", resp.StatusCode)
	}
	var shown struct {
		Capabilities []string `json:"capabilities"`
		Template     string   `json:"template"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&shown); err != nil {
		return NativeToolSupport{}, fmt.Errorf("decode model tool support: %w", err)
	}
	support := NativeToolSupport{Reason: "model does not advertise native tools"}
	for _, capability := range shown.Capabilities {
		if strings.EqualFold(capability, "tools") {
			support.Available = true
			break
		}
	}
	if support.Available && !strings.Contains(shown.Template, ".Tools") {
		support.Available = false
		support.Reason = "model template does not render tool definitions"
	}
	if support.Available {
		support.Reason = ""
	}
	c.mu.Lock()
	c.toolSupport[model] = support
	c.mu.Unlock()
	return support, nil
}

// CreativeText performs a short, no-tools completion for presentation text.
// It is intentionally separate from action planning so a more expressive
// title cannot make note IDs, paths, or action fields less reliable.
func (c *Client) CreativeText(ctx context.Context, messages []models.Message, temperature float64) (string, error) {
	c.inferenceMu.Lock()
	defer c.inferenceMu.Unlock()

	model := c.ChatModel()
	body, err := json.Marshal(models.MessageReq{
		Model:     model,
		Messages:  ollamaMessages(messages),
		Stream:    false,
		Think:     false,
		KeepAlive: "60s",
		Options: map[string]any{
			"temperature": temperature,
			"top_p":       0.95,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal creative text request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build creative text request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("call ollama creative text (model %q pulled?): %w", model, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("ollama creative text returned status %d: %s", resp.StatusCode, ollamaErrorDetail(b))
	}
	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode creative text response: %w", err)
	}
	if strings.TrimSpace(out.Message.Content) == "" {
		return "", fmt.Errorf("model returned no creative text")
	}
	return strings.TrimSpace(out.Message.Content), nil
}

func (c *Client) ChatWithToolsResult(ctx context.Context, messages []models.Message, tools []models.ToolDefinition) (ToolChatResult, error) {
	c.inferenceMu.Lock()
	defer c.inferenceMu.Unlock()

	model := c.ChatModel()
	tools = normalizedToolDefinitions(tools)
	response, err := c.openChatResponse(ctx, model, messages, tools, false,
		// Native tool planning benefits from private reasoning. Plain fallback
		// must stay quick and produce visible content for tool-less templates.
		len(tools) > 0 && shouldThink(model))
	if err != nil {
		return ToolChatResult{}, err
	}
	defer response.Body.Close()
	var out chatResponse
	if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
		return ToolChatResult{}, fmt.Errorf("decode tool chat response: %w", err)
	}
	return ToolChatResult{Message: out.Message, DoneReason: out.DoneReason}, nil
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
	// Ollama serves both chat completion and embedding requests from the same
	// local model runtime. Serializing inference prevents a batch of actions
	// from competing for a small GPU after the single planning chat finishes.
	c.inferenceMu.Lock()
	defer c.inferenceMu.Unlock()

	model := c.ChatModel()
	response, err := c.openChatResponse(ctx, model, messages, nil, true, shouldThink(model))
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	var full strings.Builder
	visible := false
	decoder := json.NewDecoder(response.Body)
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
			visible = true
			if cb.OnToken != nil {
				cb.OnToken(chunk.Message.Content)
			}
			full.WriteString(chunk.Message.Content)
		}
	}
	if !visible {
		return "", fmt.Errorf("model %q produced no visible response", model)
	}
	return full.String(), nil
}

const (
	ollamaContextOutputReserve = 1_024
	maxAutomaticContextSize    = 16_384
)

var ollamaContextErrorPattern = regexp.MustCompile(`(?i)request \((\d+) tokens?\) exceeds the available context size \((\d+) tokens?\)`)

// openChatResponse retries one Ollama request when the runtime's default
// context is smaller than the prompt. Athena grows to the smallest power of
// two that leaves a response reserve, then remembers that size for this model.
// The cap prevents an unexpectedly large prompt from silently reserving an
// unbounded amount of local RAM or VRAM.
func (c *Client) openChatResponse(ctx context.Context, model string, messages []models.Message, tools []models.ToolDefinition, stream, think bool) (*http.Response, error) {
	operation := "chat"
	if len(tools) > 0 {
		operation = "tool chat"
	}
	for attempt := 0; attempt < 2; attempt++ {
		body, err := json.Marshal(models.MessageReq{
			Model: model, Messages: ollamaMessages(messages), Tools: tools,
			Stream: stream, Think: think, KeepAlive: "60s", Options: c.localChatOptions(model),
		})
		if err != nil {
			return nil, fmt.Errorf("marshal %s request: %w", operation, err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/chat", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build %s request: %w", operation, err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := c.http.Do(request)
		if err != nil {
			return nil, fmt.Errorf("call ollama %s (model %q pulled?): %w", operation, model, err)
		}
		if response.StatusCode == http.StatusOK {
			return response, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		response.Body.Close()
		detail := ollamaErrorDetail(raw)
		if attempt == 0 && response.StatusCode == http.StatusBadRequest && c.expandContextFor(model, detail) {
			continue
		}
		return nil, fmt.Errorf("ollama %s returned status %d: %s", operation, response.StatusCode, detail)
	}
	return nil, fmt.Errorf("ollama %s context retry was exhausted", operation)
}

func (c *Client) localChatOptions(model string) map[string]any {
	options := localChatOptions()
	c.mu.RLock()
	contextSize := c.contextSize[model]
	c.mu.RUnlock()
	if contextSize > 0 {
		options["num_ctx"] = contextSize
	}
	return options
}

func (c *Client) expandContextFor(model, detail string) bool {
	matches := ollamaContextErrorPattern.FindStringSubmatch(detail)
	if len(matches) != 3 {
		return false
	}
	requested, requestedErr := strconv.Atoi(matches[1])
	available, availableErr := strconv.Atoi(matches[2])
	if requestedErr != nil || availableErr != nil || requested <= available || available <= 0 {
		return false
	}
	required := requested + ollamaContextOutputReserve
	target := 1
	for target < required && target < maxAutomaticContextSize {
		target *= 2
	}
	if target < required || target <= available || target > maxAutomaticContextSize {
		return false
	}
	c.mu.Lock()
	if c.contextSize == nil {
		c.contextSize = make(map[string]int)
	}
	if target > c.contextSize[model] {
		c.contextSize[model] = target
	}
	c.mu.Unlock()
	return true
}

// shouldThink opts into Ollama's private reasoning channel only for models
// whose names indicate support for it. Older chat models may reject or ignore
// the field, while Qwen3/DeepSeek-style models can become substantially more
// reliable on multi-step vault tasks when they get a reasoning pass.
func shouldThink(model string) bool {
	model = strings.ToLower(model)
	for _, marker := range []string{"thinking", "reasoning", "qwen3", "deepseek-r1", "deepseek-v3"} {
		if strings.Contains(model, marker) {
			return true
		}
	}
	return false
}

// ollamaMessages accommodates strict custom chat templates that require one
// leading system message and alternating user/assistant text turns. Athena's
// internal system observations must remain in chronological order: hoisting a
// prior execution result can leave two assistant plans adjacent and makes the
// model see the result before the action that produced it.
func ollamaMessages(messages []models.Message) []models.Message {
	var leadingSystem []string
	normalized := make([]models.Message, 0, len(messages)+1)
	conversationStarted := false
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if message.Role == "system" {
			if content == "" {
				continue
			}
			if !conversationStarted {
				leadingSystem = append(leadingSystem, content)
				continue
			}
			message = models.Message{
				Role: "user",
				Content: "[ATHENA ENGINE CONTINUATION — NOT USER-AUTHORED]\n" +
					content + "\n[END ATHENA ENGINE CONTINUATION]",
			}
		} else {
			conversationStarted = true
		}
		normalized = appendOllamaMessage(normalized, message)
	}
	if len(leadingSystem) > 0 {
		normalized = append([]models.Message{{Role: "system", Content: strings.Join(leadingSystem, "\n\n")}}, normalized...)
	}
	if !hasUserMessage(normalized) {
		normalized = append(normalized, models.Message{
			Role:    "user",
			Content: "[ATHENA ENGINE CONTINUATION — NOT USER-AUTHORED]\nRespond to the engine instructions above.\n[END ATHENA ENGINE CONTINUATION]",
		})
	}
	return normalized
}

// appendOllamaMessage coalesces adjacent plain text turns of the same role.
// Tool-call messages keep their exact boundaries because their IDs pair them
// with provider tool results.
func appendOllamaMessage(messages []models.Message, message models.Message) []models.Message {
	if len(messages) == 0 || (message.Role != "user" && message.Role != "assistant") || len(message.ToolCalls) > 0 || message.ToolCallID != "" {
		return append(messages, message)
	}
	last := &messages[len(messages)-1]
	if last.Role != message.Role || len(last.ToolCalls) > 0 || last.ToolCallID != "" {
		return append(messages, message)
	}
	if content := strings.TrimSpace(message.Content); content != "" {
		if strings.TrimSpace(last.Content) != "" {
			last.Content += "\n\n"
		}
		last.Content += content
	}
	return messages
}

func hasUserMessage(messages []models.Message) bool {
	for _, message := range messages {
		if message.Role == "user" && strings.TrimSpace(message.Content) != "" {
			return true
		}
	}
	return false
}

// ollamaErrorDetail unwraps the nested JSON errors returned by some custom
// templates. The raw escaped response is useful to a protocol debugger but is
// too noisy for a terminal user and can occupy most of the transcript.
func ollamaErrorDetail(body []byte) string {
	detail := strings.TrimSpace(string(body))
	for range 3 {
		var envelope struct {
			Error   json.RawMessage `json:"error"`
			Message string          `json:"message"`
		}
		if json.Unmarshal([]byte(detail), &envelope) != nil {
			break
		}
		if strings.TrimSpace(envelope.Message) != "" {
			return strings.TrimSpace(envelope.Message)
		}
		if len(envelope.Error) == 0 {
			break
		}
		var nestedText string
		if json.Unmarshal(envelope.Error, &nestedText) == nil {
			detail = strings.TrimSpace(nestedText)
			continue
		}
		var nestedError struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &nestedError) == nil && strings.TrimSpace(nestedError.Message) != "" {
			return strings.TrimSpace(nestedError.Message)
		}
		break
	}
	return detail
}

func localChatOptions() map[string]any {
	return map[string]any{
		// Low variance helps small models preserve exact IDs, paths, and JSON
		// fields. The model still gets private reasoning when supported.
		"temperature": 0.2,
		"top_p":       0.9,
	}
}

func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	c.inferenceMu.Lock()
	defer c.inferenceMu.Unlock()

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
