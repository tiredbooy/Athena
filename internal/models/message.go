package models

import "encoding/json"

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCall struct {
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type MessageReq struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	// Think is disabled because Athena needs a reliable visible response for
	// every turn; reasoning-only streams are not useful to the user or tools.
	Think     bool             `json:"think"`
	KeepAlive string           `json:"keep_alive,omitempty"`
	Tools     []ToolDefinition `json:"tools,omitempty"`
}

type EmbeddingReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type EmbeddingResp struct {
	Embedding []float32 `json:"embedding"`
}
