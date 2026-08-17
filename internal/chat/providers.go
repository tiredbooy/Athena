package chat

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/config"
)

// OAuth providers are identified by the slug of their display name, the same
// rule ProviderID applies to configured providers. Naming them once keeps
// the picker, the activation path, and the config entries in agreement.
const (
	codexProviderID    = "openai-codex"
	xaiOAuthProviderID = "xai-oauth"
	defaultCodexModel  = "gpt-5.4"
	defaultXAIModel    = "grok-4"
)

type ModelOption struct {
	ProviderID   string `json:"providerId"`
	ProviderName string `json:"providerName"`
	Model        string `json:"model"`
	Current      bool   `json:"current"`
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

func (s *Session) Models(ctx context.Context) ([]ModelOption, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loop.Models(ctx)
}
func (s *Session) SelectModel(ctx context.Context, option ModelOption) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	// A stored session that still refreshes is a working connection. Running
	// device login again would ask the user to re-approve what they already
	// approved. The token check can also fail transiently (the refresh call is
	// a network request); device login is the honest fallback in that case,
	// and the user can still cancel it.
	switch providerID {
	case codexProviderID:
		if s.loop == nil || s.loop.oauth == nil {
			return "", fmt.Errorf("OpenAI subscription sign-in is unavailable")
		}
		model := s.loop.savedModel(codexProviderID, defaultCodexModel)
		if _, err := s.loop.oauth.Credentials(ctx); err == nil {
			if err := s.loop.activateOpenAISubscription(model); err != nil {
				return "", err
			}
			return "Reused the saved ChatGPT Plus / Pro session.", nil
		}
		if err := s.loop.oauth.RunDeviceLogin(ctx, onStatus); err != nil {
			return "", err
		}
		if err := s.loop.activateOpenAISubscription(model); err != nil {
			return "", err
		}
		return "Connected ChatGPT Plus / Pro through Codex device login.", nil
	case xaiOAuthProviderID:
		if s.loop == nil || s.loop.xaiOAuth == nil {
			return "", fmt.Errorf("xAI subscription sign-in is unavailable")
		}
		model := s.loop.savedModel(xaiOAuthProviderID, defaultXAIModel)
		if _, err := s.loop.xaiOAuth.AccessToken(ctx); err == nil {
			if err := s.loop.activateXAISubscription(model); err != nil {
				return "", err
			}
			return "Reused the saved Grok Pro / SuperGrok session.", nil
		}
		if err := s.loop.xaiOAuth.RunDeviceLogin(ctx, onStatus); err != nil {
			return "", err
		}
		if err := s.loop.activateXAISubscription(model); err != nil {
			return "", err
		}
		return "Connected Grok Pro / SuperGrok through xAI device login.", nil
	default:
		return "", fmt.Errorf("OAuth is unavailable for provider %q", providerID)
	}
}

// connectedProvider is a provider the user can switch to right now, without
// authenticating again.
type connectedProvider struct {
	id    string
	name  string
	model string
}

// connectedProviders lists every switchable provider: the ones constructed
// from config at startup, plus any OAuth session that survives only as a token
// file. A stored token is a real connection — a config reset, or a fresh
// install that inherits ~/.codex, must not cost the user another device login.
// The order is stable so the picker does not reshuffle between calls.
func (l *Loop) connectedProviders() []connectedProvider {
	out := make([]connectedProvider, 0, len(l.providers)+2)
	for id, provider := range l.providers {
		out = append(out, connectedProvider{id: id, name: provider.Name(), model: provider.ChatModel()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })

	if _, built := l.providers[codexProviderID]; !built && l.oauth != nil && l.oauth.Connected() {
		out = append(out, connectedProvider{id: codexProviderID, name: "OpenAI Codex", model: l.savedModel(codexProviderID, defaultCodexModel)})
	}
	if _, built := l.providers[xaiOAuthProviderID]; !built && l.xaiOAuth != nil && l.xaiOAuth.Connected() {
		out = append(out, connectedProvider{id: xaiOAuthProviderID, name: "xAI OAuth", model: l.savedModel(xaiOAuthProviderID, defaultXAIModel)})
	}
	return out
}

// savedModel returns the model already configured for a provider so that
// switching back does not silently reset the user's choice to a default.
func (l *Loop) savedModel(providerID, fallback string) string {
	if l.config != nil {
		for _, entry := range l.config.Providers {
			if ProviderID(entry.Name) == providerID && strings.TrimSpace(entry.ChatModel) != "" {
				return entry.ChatModel
			}
		}
	}
	return fallback
}

// Models lists models across every connected provider so the user can switch
// back to one they already authenticated, instead of being trapped on whatever
// is active.
//
// Only the active provider's live catalog is fetched. Querying every remote
// provider would add a network round trip per entry, and one unreachable
// service would stall the whole picker — the HTTP clients have no timeout yet
// (A-05). Other providers contribute their saved model, which is all that is
// needed to switch; their full catalog appears once they are active.
// ponytail: saved-model row per inactive provider; fetch all catalogs
// concurrently once the clients have timeouts and users need to pick a
// non-default model without switching first.
func (l *Loop) Models(ctx context.Context) ([]ModelOption, error) {
	activeID := l.activeProviderID()
	options := make([]ModelOption, 0, 8)

	var catalogErr error
	if active, built := l.providers[activeID]; built {
		available, err := active.ChatModels(ctx)
		if err != nil {
			catalogErr = fmt.Errorf("list %s models: %w", active.Name(), err)
		}
		for _, model := range available {
			options = append(options, ModelOption{ProviderID: activeID, ProviderName: active.Name(), Model: model.Name, Current: model.Name == active.ChatModel()})
		}
	}
	activeListed := len(options) > 0

	for _, entry := range l.connectedProviders() {
		// The active provider is already represented by its live catalog. If
		// that catalog is missing, fall through so the user still sees where
		// they are instead of the provider vanishing from its own picker.
		if entry.id == activeID && activeListed {
			continue
		}
		if entry.model == "" {
			continue
		}
		options = append(options, ModelOption{ProviderID: entry.id, ProviderName: entry.name, Model: entry.model, Current: entry.id == activeID})
	}

	if len(options) == 0 {
		if catalogErr != nil {
			return nil, fmt.Errorf("%w; verify its address and credentials, then reconnect with /connect", catalogErr)
		}
		return nil, fmt.Errorf("no chat provider is connected; add one with /connect")
	}
	if catalogErr != nil {
		// Failing the whole call here would trap the user on a broken provider
		// with no way to pick a working one, which is the bug this function
		// exists to fix. The other providers are still listed and usable.
		fmt.Fprintf(os.Stderr, "warning: %v; listing saved models instead\n", catalogErr)
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
	entry := config.ProviderConfig{Name: "OpenAI Codex", Type: "openai_codex", ChatModel: model}
	provider, err := l.buildProvider(entry)
	if err != nil {
		return err
	}
	l.providers[codexProviderID] = provider
	l.ai = provider
	if l.config == nil {
		return nil
	}
	found := false
	for i := range l.config.Providers {
		if ProviderID(l.config.Providers[i].Name) == codexProviderID {
			l.config.Providers[i].ChatModel = model
			found = true
		}
	}
	if !found {
		l.config.Providers = append(l.config.Providers, entry)
	}
	l.config.ActiveProvider = codexProviderID
	if err := l.config.Save(); err != nil {
		return fmt.Errorf("save OpenAI subscription connection: %w", err)
	}
	return nil
}

func (l *Loop) activateXAISubscription(model string) error {
	entry := config.ProviderConfig{Name: "xAI OAuth", Type: "xai_oauth", BaseURL: "https://api.x.ai/v1", ChatModel: model}
	provider, err := l.buildProvider(entry)
	if err != nil {
		return err
	}
	l.providers[xaiOAuthProviderID] = provider
	l.ai = provider
	if l.config == nil {
		return nil
	}
	found := false
	for i := range l.config.Providers {
		if ProviderID(l.config.Providers[i].Name) == xaiOAuthProviderID {
			l.config.Providers[i] = entry
			found = true
			break
		}
	}
	if !found {
		l.config.Providers = append(l.config.Providers, entry)
	}
	l.config.ActiveProvider = xaiOAuthProviderID
	if err := l.config.Save(); err != nil {
		return fmt.Errorf("save xAI subscription connection: %w", err)
	}
	return nil
}

func (l *Loop) SelectModel(_ context.Context, providerID, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model cannot be empty")
	}
	provider, ok := l.providers[providerID]
	if !ok {
		// The provider was never constructed because config has no entry for
		// it, but its OAuth token file survives. Rebuilding it from that token
		// is what makes switching back free of another device login; the
		// adapter refreshes the token itself on its next request.
		if err := l.activateStoredOAuth(providerID, model); err != nil {
			return "", err
		}
		return fmt.Sprintf("Using %s via %s", model, l.ai.Name()), nil
	}
	provider.SetChatModel(model)
	l.ai = provider
	if l.config != nil {
		l.config.ActiveProvider = providerID
		if providerID == "ollama" {
			l.config.ChatModel = model
		} else {
			for i := range l.config.Providers {
				if ProviderID(l.config.Providers[i].Name) == providerID {
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

// activateStoredOAuth rebuilds a provider whose only remaining record is its
// token file. It deliberately refuses anything else: an unknown id must not
// silently become a new connection.
func (l *Loop) activateStoredOAuth(providerID, model string) error {
	switch providerID {
	case codexProviderID:
		if l.oauth == nil || !l.oauth.Connected() {
			return fmt.Errorf("provider %q is not connected", providerID)
		}
		return l.activateOpenAISubscription(model)
	case xaiOAuthProviderID:
		if l.xaiOAuth == nil || !l.xaiOAuth.Connected() {
			return fmt.Errorf("provider %q is not connected", providerID)
		}
		return l.activateXAISubscription(model)
	default:
		return fmt.Errorf("provider %q is not connected", providerID)
	}
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
	// Before anything is stored: the API key below is sent to this address, so a
	// typo or a hostile value exfiltrates the user's credential.
	if err := validateBaseURL(input.BaseURL); err != nil {
		return "", err
	}
	id := ProviderID(input.Name)
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
		if ProviderID(l.config.Providers[i].Name) == id {
			l.config.Providers[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		l.config.Providers = append(l.config.Providers, entry)
	}
	// The key was written to the store above, so the builder reads back exactly
	// what was typed — and, when nothing was typed, the key a previous /connect
	// stored instead of overwriting the adapter's credential with nothing.
	provider, err := l.buildProvider(entry)
	if err != nil {
		return "", err
	}
	l.providers[id] = provider
	if _, err := l.SelectModel(context.Background(), id, entry.ChatModel); err != nil {
		return "", err
	}
	return fmt.Sprintf("Connected %s. Using %s.", entry.Name, entry.ChatModel), nil
}

// validateBaseURL accepts https anywhere, and plain http only to loopback,
// where local model servers (LM Studio, Ollama, vLLM, llama.cpp) live and the
// request never leaves the machine. http to any other host would put the API
// key on the wire in cleartext.
//
// The host comes from net/url, never from string matching: "http://localhost@evil.com"
// and "http://evil.com#@localhost" both have host evil.com and must be refused.
func validateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse base URL %q: %w", raw, err)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("base URL %q needs a host, for example https://api.example.com/v1", raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if strings.EqualFold(host, "localhost") {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("base URL %q would send the API key to %q in cleartext; use https, or http only for a local server on localhost", raw, host)
	default:
		return fmt.Errorf("base URL %q must start with https:// (or http:// for a local server on localhost)", raw)
	}
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

// ProviderID is the slug a provider is stored and looked up by, everywhere: in
// l.providers, in the credential store, and in config.active_provider. It is
// exported because the composition root registers providers under the same
// slug this package resolves them by — two copies of this rule would let a
// provider registered at startup become unreachable at runtime.
func ProviderID(name string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-")
}

// ProviderCredentials carries the secrets a saved provider entry needs to
// become a live adapter. config.yaml holds none of them: API keys come from the
// credential store (or, when it has none, from the environment inside the
// adapter), and the OAuth providers from their token files.
type ProviderCredentials struct {
	APIKeys *config.CredentialStore
	Codex   *ai.CodexOAuth
	XAI     *ai.XAIOAuth
}

// BuildProvider turns one saved provider entry into a live adapter. Startup and
// /connect both go through it because they used to construct adapters
// independently and had already drifted: startup applied the stored API key
// while /connect applied only the one just typed, so reconnecting a provider
// without retyping its key produced an adapter with no credential at all.
func BuildProvider(entry config.ProviderConfig, credentials ProviderCredentials) (ai.ChatProvider, error) {
	// Every branch below that has a base URL also attaches a credential to it,
	// so the URL has to clear the same bar /connect applies. Validating only in
	// Connect left startup unguarded: config.yaml is an ordinary file a user can
	// hand-edit, and a synced or mistyped entry would hand the stored API key to
	// whatever host it named, on the first request after launch.
	if strings.TrimSpace(entry.BaseURL) != "" {
		if err := validateBaseURL(entry.BaseURL); err != nil {
			return nil, fmt.Errorf("provider %q: %w", entry.Name, err)
		}
	}
	switch entry.Type {
	case "openai_codex":
		if credentials.Codex == nil {
			return nil, fmt.Errorf("provider %q needs OpenAI subscription credentials", entry.Name)
		}
		return ai.NewCodexProvider(credentials.Codex, entry.ChatModel), nil
	case "xai_oauth":
		if credentials.XAI == nil {
			return nil, fmt.Errorf("provider %q needs xAI subscription credentials", entry.Name)
		}
		// The token, not a key: the adapter asks for a fresh one per request.
		provider := ai.NewOpenAICompatibleProvider(entry.Name, entry.BaseURL, "", entry.ChatModel)
		provider.SetTokenSource(credentials.XAI.AccessToken)
		return provider, nil
	case "anthropic":
		provider := ai.NewAnthropicProvider(entry.Name, entry.BaseURL, entry.APIKeyEnv, entry.ChatModel)
		provider.SetAPIKey(credentials.APIKeys.APIKey(ProviderID(entry.Name)))
		return provider, nil
	default:
		// An unrecognised type falls back to the OpenAI-compatible adapter
		// rather than failing: "openai", "openai_compatible", and hand-written
		// config values all speak that wire format.
		provider := ai.NewOpenAICompatibleProvider(entry.Name, entry.BaseURL, entry.APIKeyEnv, entry.ChatModel)
		provider.SetAPIKey(credentials.APIKeys.APIKey(ProviderID(entry.Name)))
		return provider, nil
	}
}

func (l *Loop) buildProvider(entry config.ProviderConfig) (ai.ChatProvider, error) {
	return BuildProvider(entry, ProviderCredentials{APIKeys: l.credentials, Codex: l.oauth, XAI: l.xaiOAuth})
}
