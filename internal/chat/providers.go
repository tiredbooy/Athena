package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/config"
)

type ModelOption struct {
	ProviderID   string
	ProviderName string
	Model        string
	Current      bool
}

type ConnectionInput struct {
	Name      string
	Type      string
	BaseURL   string
	APIKeyEnv string
	ChatModel string
}

func (s *Session) Models(ctx context.Context) ([]ModelOption, error) { return s.loop.Models(ctx) }
func (s *Session) SelectModel(ctx context.Context, option ModelOption) (string, error) {
	return s.loop.SelectModel(ctx, option.ProviderID, option.Model)
}
func (s *Session) Connect(input ConnectionInput) (string, error) { return s.loop.Connect(input) }
func (s *Session) StartOpenAISubscription(ctx context.Context) (string, error) {
	return s.loop.StartOpenAISubscription(ctx)
}

func (l *Loop) Models(ctx context.Context) ([]ModelOption, error) {
	available, err := l.ai.ChatModels(ctx)
	if err != nil {
		return nil, err
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("%s returned no models; verify its address and credentials, then reconnect with /connect", l.ai.Name())
	}
	options := make([]ModelOption, 0, len(available))
	for _, model := range available {
		options = append(options, ModelOption{ProviderID: l.activeProviderID(), ProviderName: l.ai.Name(), Model: model.Name, Current: model.Name == l.ai.ChatModel()})
	}
	return options, nil
}

func (l *Loop) StartOpenAISubscription(ctx context.Context) (string, error) {
	if l.oauth == nil {
		return "", fmt.Errorf("OpenAI subscription sign-in is unavailable")
	}
	url, result, err := l.oauth.Start(ctx)
	if err != nil {
		return "", err
	}
	go func() {
		if err := <-result; err != nil {
			return
		}
		provider := ai.NewCodexProvider(l.oauth, "gpt-5.2")
		l.providers["openai-codex"] = provider
		l.ai = provider
		if l.config != nil {
			found := false
			for i := range l.config.Providers {
				if providerIDFor(l.config.Providers[i]) == "openai-codex" {
					l.config.Providers[i].ChatModel = "gpt-5.2"
					found = true
				}
			}
			if !found {
				l.config.Providers = append(l.config.Providers, config.ProviderConfig{Name: "OpenAI Codex", Type: "openai_codex", ChatModel: "gpt-5.2"})
			}
			l.config.ActiveProvider = "openai-codex"
			_ = l.config.Save()
		}
	}()
	return url, nil
}

func (l *Loop) SelectModel(_ context.Context, providerID, model string) (string, error) {
	provider, ok := l.providers[providerID]
	if !ok {
		return "", fmt.Errorf("provider %q is not connected", providerID)
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model cannot be empty")
	}
	provider.SetChatModel(model)
	l.ai = provider
	if l.config != nil {
		l.config.ActiveProvider = providerID
		if providerID == "ollama" {
			l.config.ChatModel = model
		} else {
			for i := range l.config.Providers {
				if providerIDFor(l.config.Providers[i]) == providerID {
					l.config.Providers[i].ChatModel = model
					break
				}
			}
		}
		if err := l.config.Save(); err != nil {
			return "", fmt.Errorf("save selected model: %w", err)
		}
	}
	return fmt.Sprintf("Using %s via %s", model, provider.Name()), nil
}

func (l *Loop) Connect(input ConnectionInput) (string, error) {
	if l.config == nil {
		return "", fmt.Errorf("provider configuration is unavailable")
	}
	input.Name, input.Type, input.BaseURL, input.APIKeyEnv, input.ChatModel = strings.TrimSpace(input.Name), strings.TrimSpace(input.Type), strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), strings.TrimSpace(input.APIKeyEnv), strings.TrimSpace(input.ChatModel)
	if input.Name == "" || input.BaseURL == "" || input.ChatModel == "" {
		return "", fmt.Errorf("provider name, base URL, and chat model are required")
	}
	if input.Type != "openai" && input.Type != "openai_compatible" && input.Type != "anthropic" {
		return "", fmt.Errorf("unsupported provider type %q", input.Type)
	}
	id := providerIDFor(config.ProviderConfig{Name: input.Name})
	if id == "ollama" {
		return "", fmt.Errorf("%q is reserved for the built-in Ollama provider", input.Name)
	}
	entry := config.ProviderConfig{Name: input.Name, Type: input.Type, BaseURL: input.BaseURL, APIKeyEnv: input.APIKeyEnv, ChatModel: input.ChatModel}
	replaced := false
	for i := range l.config.Providers {
		if providerIDFor(l.config.Providers[i]) == id {
			l.config.Providers[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		l.config.Providers = append(l.config.Providers, entry)
	}
	if entry.Type == "anthropic" {
		l.providers[id] = ai.NewAnthropicProvider(entry.Name, entry.BaseURL, entry.APIKeyEnv, entry.ChatModel)
	} else {
		l.providers[id] = ai.NewOpenAICompatibleProvider(entry.Name, entry.BaseURL, entry.APIKeyEnv, entry.ChatModel)
	}
	_, err := l.SelectModel(context.Background(), id, entry.ChatModel)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Connected %s. Using %s.", entry.Name, entry.ChatModel), nil
}

func (l *Loop) activeProviderID() string {
	if l.config != nil && l.config.ActiveProvider != "" {
		return l.config.ActiveProvider
	}
	return "ollama"
}

func providerIDFor(provider config.ProviderConfig) string {
	id := strings.ToLower(strings.TrimSpace(provider.Name))
	var out strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-")
}
