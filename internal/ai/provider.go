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

// RequiredToolProvider is an optional provider capability for planning turns
// that must produce a tool decision rather than unstructured prose. Providers
// implement it only when their API has a native required-tool mode.
type RequiredToolProvider interface {
	ChatWithRequiredToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ToolChatResult, error)
}

type NativeToolSupport struct {
	Available bool
	Reason    string
}

// normalizedToolDefinitions keeps parameterless functions valid across model
// APIs. A nil Go map otherwise becomes JSON null, but function inputs are JSON
// Schemas and therefore need an object schema even when they accept no fields.
func normalizedToolDefinitions(definitions []models.ToolDefinition) []models.ToolDefinition {
	if len(definitions) == 0 {
		return definitions
	}
	normalized := make([]models.ToolDefinition, len(definitions))
	copy(normalized, definitions)
	for i := range normalized {
		normalized[i].Function.Parameters = normalizedFunctionParameters(normalized[i].Function.Parameters)
	}
	return normalized
}

func normalizedFunctionParameters(parameters map[string]any) map[string]any {
	if parameters == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if schemaType, _ := parameters["type"].(string); schemaType != "object" {
		return parameters
	}
	if properties, ok := parameters["properties"].(map[string]any); ok && properties != nil {
		return parameters
	}
	normalized := make(map[string]any, len(parameters)+1)
	for key, value := range parameters {
		normalized[key] = value
	}
	normalized["properties"] = map[string]any{}
	return normalized
}
