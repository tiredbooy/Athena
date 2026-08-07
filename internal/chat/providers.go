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
	Name      string `json:"name"`
	Type      string `json:"type"`
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	ChatModel string `json:"chat_model"`
}

type ProviderPreset struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Detail      string `json:"detail"`
	Type        string `json:"type"`
	Auth        string `json:"auth"`
	Name        string `json:"name,omitempty"`
	BaseURL     string `json:"base_url,omitempty"`
	APIKeyEnv   string `json:"api_key_env,omitempty"`
	ChatModel   string `json:"chat_model,omitempty"`
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable,omitempty"`
}

func ProviderPresets() []ProviderPreset {
	return []ProviderPreset{
		{ID: "openai-codex", Label: "ChatGPT Plus / Pro", Detail: "OpenAI Codex device login", Type: "openai_codex", Auth: "oauth", Name: "OpenAI Codex", ChatModel: "gpt-5.4", Available: true},
		{ID: "openai", Label: "OpenAI API", Detail: "API key", Type: "openai", Auth: "api_key", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY", ChatModel: "gpt-5.4", Available: true},
		{ID: "anthropic", Label: "Anthropic", Detail: "API key", Type: "anthropic", Auth: "api_key", Name: "Anthropic", BaseURL: "https://api.anthropic.com/v1", APIKeyEnv: "ANTHROPIC_API_KEY", ChatModel: "claude-sonnet-4-5", Available: true},
		{ID: "xai-oauth", Label: "Grok Pro / SuperGrok", Detail: "xAI device login", Type: "xai_oauth", Auth: "oauth", Name: "xAI OAuth", BaseURL: "https://api.x.ai/v1", ChatModel: "grok-4", Available: true},
		{ID: "xai", Label: "xAI / Grok API", Detail: "API key", Type: "openai_compatible", Auth: "api_key", Name: "xAI", BaseURL: "https://api.x.ai/v1", APIKeyEnv: "XAI_API_KEY", ChatModel: "grok-4", Available: true},
		{ID: "openrouter", Label: "OpenRouter", Detail: "API key", Type: "openai_compatible", Auth: "api_key", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY", ChatModel: "openai/gpt-5.4", Available: true},
		{ID: "ollama", Label: "Restore built-in Ollama", Detail: "local", Type: "ollama", Auth: "none", Available: true},
		{ID: "custom", Label: "Custom compatible API", Detail: "name, URL, key, model", Type: "openai_compatible", Auth: "api_key", Name: "", BaseURL: "http://localhost:1234/v1", ChatModel: "", Available: true},
	}
}

func (s *Session) ProviderPresets() []ProviderPreset { return ProviderPresets() }

func (s *Session) Models(ctx context.Context) ([]ModelOption, error) { return s.loop.Models(ctx) }
func (s *Session) SelectModel(ctx context.Context, option ModelOption) (string, error) {
	return s.loop.SelectModel(ctx, option.ProviderID, option.Model)
}
func (s *Session) Connect(input ConnectionInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loop.Connect(input)
}
func (s *Session) StartOpenAISubscription(ctx context.Context) (string, error) {
	return s.loop.StartOpenAISubscription(ctx)
}

func (s *Session) ConnectOAuth(ctx context.Context, providerID string, onStatus func(string)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch providerID {
	case "openai-codex":
		if s.loop == nil || s.loop.oauth == nil {
			return "", fmt.Errorf("OpenAI subscription sign-in is unavailable")
		}
		if err := s.loop.oauth.RunDeviceLogin(ctx, onStatus); err != nil {
			return "", err
		}
		if err := s.loop.activateOpenAISubscription("gpt-5.4"); err != nil {
			return "", err
		}
		return "Connected ChatGPT Plus / Pro through Codex device login.", nil
	case "xai-oauth":
		if s.loop == nil || s.loop.xaiOAuth == nil {
			return "", fmt.Errorf("xAI subscription sign-in is unavailable")
		}
		if err := s.loop.xaiOAuth.RunDeviceLogin(ctx, onStatus); err != nil {
			return "", err
		}
		if err := s.loop.activateXAISubscription("grok-4"); err != nil {
			return "", err
		}
		return "Connected Grok Pro / SuperGrok through xAI device login.", nil
	default:
		return "", fmt.Errorf("OAuth is unavailable for provider %q", providerID)
	}
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
		_ = l.activateOpenAISubscription("gpt-5.4")
	}()
	return url, nil
}

func (l *Loop) activateOpenAISubscription(model string) error {
	provider := ai.NewCodexProvider(l.oauth, model)
	l.providers["openai-codex"] = provider
	l.ai = provider
	if l.config == nil {
		return nil
	}
	found := false
	for i := range l.config.Providers {
		if providerIDFor(l.config.Providers[i]) == "openai-codex" {
			l.config.Providers[i].ChatModel = model
			found = true
		}
	}
	if !found {
		l.config.Providers = append(l.config.Providers, config.ProviderConfig{Name: "OpenAI Codex", Type: "openai_codex", ChatModel: model})
	}
	l.config.ActiveProvider = "openai-codex"
	if err := l.config.Save(); err != nil {
		return fmt.Errorf("save OpenAI subscription connection: %w", err)
	}
	return nil
}

func (l *Loop) activateXAISubscription(model string) error {
	if l.xaiOAuth == nil {
		return fmt.Errorf("xAI subscription sign-in is unavailable")
	}
	const providerID = "xai-oauth"
	provider := ai.NewOpenAICompatibleProvider("xAI OAuth", "https://api.x.ai/v1", "", model)
	provider.SetTokenSource(l.xaiOAuth.AccessToken)
	l.providers[providerID] = provider
	l.ai = provider
	if l.config == nil {
		return nil
	}
	entry := config.ProviderConfig{Name: "xAI OAuth", Type: "xai_oauth", BaseURL: "https://api.x.ai/v1", ChatModel: model}
	found := false
	for i := range l.config.Providers {
		if providerIDFor(l.config.Providers[i]) == providerID {
			l.config.Providers[i] = entry
			found = true
			break
		}
	}
	if !found {
		l.config.Providers = append(l.config.Providers, entry)
	}
	l.config.ActiveProvider = providerID
	if err := l.config.Save(); err != nil {
		return fmt.Errorf("save xAI subscription connection: %w", err)
	}
	return nil
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
	input.Name, input.Type, input.BaseURL, input.APIKeyEnv, input.APIKey, input.ChatModel = strings.TrimSpace(input.Name), strings.TrimSpace(input.Type), strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), strings.TrimSpace(input.APIKeyEnv), strings.TrimSpace(input.APIKey), strings.TrimSpace(input.ChatModel)
	if input.Type == "ollama" {
		return l.restoreOllama()
	}
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
	if input.APIKey != "" {
		if l.credentials == nil {
			return "", fmt.Errorf("provider credential storage is unavailable")
		}
		if err := l.credentials.SaveAPIKey(id, input.APIKey); err != nil {
			return "", err
		}
	}
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
		provider := ai.NewAnthropicProvider(entry.Name, entry.BaseURL, entry.APIKeyEnv, entry.ChatModel)
		provider.SetAPIKey(input.APIKey)
		l.providers[id] = provider
	} else {
		provider := ai.NewOpenAICompatibleProvider(entry.Name, entry.BaseURL, entry.APIKeyEnv, entry.ChatModel)
		provider.SetAPIKey(input.APIKey)
		l.providers[id] = provider
	}
	_, err := l.SelectModel(context.Background(), id, entry.ChatModel)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Connected %s. Using %s.", entry.Name, entry.ChatModel), nil
}

func (l *Loop) restoreOllama() (string, error) {
	if l.config == nil {
		return "", fmt.Errorf("provider configuration is unavailable")
	}
	client, ok := l.providers["ollama"].(*ai.Client)
	if !ok {
		return "", fmt.Errorf("built-in Ollama provider is unavailable")
	}
	l.config.RestoreOllamaDefaults()
	client.SetHost(l.config.OllamaHost)
	client.SetChatModel(l.config.ChatModel)
	l.ai = client
	if err := l.config.Save(); err != nil {
		return "", fmt.Errorf("save restored Ollama settings: %w", err)
	}
	return fmt.Sprintf("Restored built-in Ollama (%s). Using %s.", l.config.OllamaHost, l.config.ChatModel), nil
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
