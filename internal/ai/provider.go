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

// CreativeTextProvider is optional. It lets the application use a separate
// higher-temperature completion for presentation-only text such as a note
// title without weakening the low-variance action planner.
type CreativeTextProvider interface {
	CreativeText(context.Context, []models.Message, float64) (string, error)
}

// NativeToolSupportProvider is optional because remote providers can expose
// tools without an Ollama-style model manifest. Local sessions use it to avoid
// spending an inference pass on a tool schema a model template cannot render.
type NativeToolSupportProvider interface {
	NativeToolSupport(context.Context) (NativeToolSupport, error)
}

type NativeToolSupport struct {
	Available bool
	Reason    string
}
