package models

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type MessageReq struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	// Think is disabled because Athena needs a reliable visible response for
	// every turn; reasoning-only streams are not useful to the user or tools.
	Think     bool   `json:"think"`
	KeepAlive string `json:"keep_alive,omitempty"`
}

type EmbeddingReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type EmbeddingResp struct {
	Embedding []float32 `json:"embedding"`
}
