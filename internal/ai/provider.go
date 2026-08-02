package ai

import (
	"context"

	"github.com/tiredbooy/internal/models"
)

// ChatProvider is the narrow boundary between Athena's application layer and
// a model API. Embeddings stay outside this interface because changing vector
// providers also requires re-indexing the vault.
type ChatProvider interface {
	Name() string
	ChatModel() string
	SetChatModel(string)
	ChatModels(context.Context) ([]ModelInfo, error)
	ChatWithToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ToolChatResult, error)
	StreamChatWith(context.Context, []models.Message, StreamCallbacks) (string, error)
}
