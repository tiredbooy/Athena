package models

type Message struct {
	Role	string `json:"role"`
	Content	string `json:"content"`
}

type MessageReq struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
	KeepAlive string    `json:"keep_alive,omitempty"`
}

type EmbeddingReq struct {
	Model string `json:"model"`
	Prompt string `json:"prompt"`
}

type EmbeddingResp struct {
	Embedding []float32 `json:"embedding"`
}