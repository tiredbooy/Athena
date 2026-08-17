package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/config"
	"github.com/tiredbooy/internal/models"
)

// pickerProvider is a minimal ChatProvider for exercising the model picker.
// catalog is what the live /v1/models call would return; catalogErr simulates
// an unreachable service.
type pickerProvider struct {
	name       string
	model      string
	catalog    []string
	catalogErr error
}

func (p *pickerProvider) Name() string          { return p.name }
func (p *pickerProvider) ChatModel() string     { return p.model }
func (p *pickerProvider) SetChatModel(m string) { p.model = m }
func (p *pickerProvider) ChatModels(context.Context) ([]ai.ModelInfo, error) {
	if p.catalogErr != nil {
		return nil, p.catalogErr
	}
	out := make([]ai.ModelInfo, 0, len(p.catalog))
	for _, name := range p.catalog {
		out = append(out, ai.ModelInfo{Name: name})
	}
	return out, nil
}
func (p *pickerProvider) ChatWithToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ai.ToolChatResult, error) {
	return ai.ToolChatResult{}, errors.New("not used")
}
func (p *pickerProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", errors.New("not used")
}

func providersByID(options []ModelOption) map[string][]string {
	out := make(map[string][]string)
	for _, option := range options {
		out[option.ProviderID] = append(out[option.ProviderID], option.Model)
	}
	return out
}

// The bug this guards: after "Restore built-in Ollama", the picker showed only
// Ollama, so a user with valid Grok/Codex tokens had no way back except
// /connect and another device login.
func TestModelsListsEveryConnectedProvider(t *testing.T) {
	ollama := &pickerProvider{name: "Ollama", model: "qwen3:1.7b", catalog: []string{"qwen3:1.7b", "gemma3:4b"}}
	grok := &pickerProvider{name: "xAI OAuth", model: "grok-4"}
	loop := &Loop{
		ai:        ollama,
		providers: map[string]ai.ChatProvider{"ollama": ollama, xaiOAuthProviderID: grok},
		config: &config.Config{
			ActiveProvider: "ollama",
			Providers:      []config.ProviderConfig{{Name: "xAI OAuth", Type: "xai_oauth", ChatModel: "grok-4"}},
		},
	}

	options, err := loop.Models(context.Background())
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	byProvider := providersByID(options)
	if len(byProvider["ollama"]) != 2 {
		t.Fatalf("active provider models = %v, want its full catalog", byProvider["ollama"])
	}
	if got := byProvider[xaiOAuthProviderID]; len(got) != 1 || got[0] != "grok-4" {
		t.Fatalf("xAI models = %v, want the saved grok-4 so the user can switch back", got)
	}
}

// A broken active provider must not hide the working ones, or the user is
// trapped on it.
func TestModelsStillListsOthersWhenActiveCatalogFails(t *testing.T) {
	broken := &pickerProvider{name: "Ollama", model: "qwen3:1.7b", catalogErr: errors.New("connection refused")}
	grok := &pickerProvider{name: "xAI OAuth", model: "grok-4"}
	loop := &Loop{
		ai:        broken,
		providers: map[string]ai.ChatProvider{"ollama": broken, xaiOAuthProviderID: grok},
		config:    &config.Config{ActiveProvider: "ollama"},
	}

	options, err := loop.Models(context.Background())
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	byProvider := providersByID(options)
	if got := byProvider[xaiOAuthProviderID]; len(got) != 1 {
		t.Fatalf("xAI models = %v, want the saved model despite the active provider being down", got)
	}
	// The active provider keeps a row so it does not vanish from its own picker.
	if got := byProvider["ollama"]; len(got) != 1 || got[0] != "qwen3:1.7b" {
		t.Fatalf("ollama models = %v, want its saved model as a fallback row", got)
	}
}

func TestModelsErrorsWhenNothingIsConnected(t *testing.T) {
	loop := &Loop{providers: map[string]ai.ChatProvider{}, config: &config.Config{}}
	if _, err := loop.Models(context.Background()); err == nil {
		t.Fatal("expected an error when no provider is connected")
	}
}

// P-02: choosing an already-connected provider switches immediately. No
// device login, no /connect.
func TestSelectModelSwitchesBackWithoutReconnecting(t *testing.T) {
	ollama := &pickerProvider{name: "Ollama", model: "qwen3:1.7b"}
	grok := &pickerProvider{name: "xAI OAuth", model: "grok-4"}
	cfg := &config.Config{
		ActiveProvider: "ollama",
		Providers:      []config.ProviderConfig{{Name: "xAI OAuth", Type: "xai_oauth", ChatModel: "grok-4"}},
	}
	loop := &Loop{ai: ollama, providers: map[string]ai.ChatProvider{"ollama": ollama, xaiOAuthProviderID: grok}, config: cfg}

	// A fixture Config has no file behind it, so Save refuses. That refusal is
	// the point: the switch itself must still have happened in memory.
	if _, err := loop.SelectModel(context.Background(), xaiOAuthProviderID, "grok-4"); err == nil {
		t.Fatal("expected saving an unloaded config to be refused")
	}
	if loop.ai != ai.ChatProvider(grok) {
		t.Fatalf("active provider = %s, want xAI OAuth", loop.ai.Name())
	}
	if cfg.ActiveProvider != xaiOAuthProviderID {
		t.Fatalf("active provider id = %q, want %q", cfg.ActiveProvider, xaiOAuthProviderID)
	}
}

func TestSelectModelRejectsUnknownProvider(t *testing.T) {
	ollama := &pickerProvider{name: "Ollama", model: "qwen3:1.7b"}
	loop := &Loop{ai: ollama, providers: map[string]ai.ChatProvider{"ollama": ollama}, config: &config.Config{}}
	if _, err := loop.SelectModel(context.Background(), "not-connected", "some-model"); err == nil {
		t.Fatal("expected an unknown provider to be refused, not silently connected")
	}
}

// A-06: the API key is sent to whatever base URL /connect was given, so a typo
// or a hostile value exfiltrates it. https is fine anywhere; plain http only to
// loopback, where local model servers run.
func TestValidateBaseURL(t *testing.T) {
	allowed := []string{
		"https://api.openai.com/v1",
		"https://api.example.com:8443/v1",
		"http://localhost:1234/v1",
		"http://LocalHost:11434",
		"http://127.0.0.1:8000/v1",
		"http://127.5.5.5:8080",
		"http://[::1]:8080/v1",
	}
	for _, raw := range allowed {
		if err := validateBaseURL(raw); err != nil {
			t.Errorf("validateBaseURL(%q) = %v, want it accepted", raw, err)
		}
	}

	rejected := []string{
		"http://api.example.com/v1",    // cleartext key over the network
		"http://192.168.1.50:1234/v1",  // LAN is not loopback
		"http://localhost@evil.com/v1", // userinfo, real host is evil.com
		"http://evil.com#@localhost",   // fragment, real host is evil.com
		"http://evil.com/localhost",    // path, real host is evil.com
		"https:///v1",                  // no host at all
		"ftp://example.com/v1",
		"file:///etc/passwd",
		"localhost:1234/v1", // no scheme; parses as scheme "localhost"
		"not a url",
	}
	for _, raw := range rejected {
		if err := validateBaseURL(raw); err == nil {
			t.Errorf("validateBaseURL(%q) = nil, want it refused", raw)
		}
	}
}

// The key must not reach disk when the URL is refused: Connect saves it before
// it appends the provider entry, so validation has to run earlier than that.
func TestConnectRejectsUnsafeBaseURLBeforeStoringTheAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	credentials, err := config.LoadCredentialStore()
	if err != nil {
		t.Fatalf("load credential store: %v", err)
	}
	cfg := &config.Config{}
	loop := &Loop{providers: map[string]ai.ChatProvider{}, config: cfg, credentials: credentials}

	if _, err := loop.Connect(ConnectionInput{
		Name:      "Sketchy",
		Type:      "openai_compatible",
		BaseURL:   "http://api.example.com/v1",
		APIKey:    "sk-secret",
		ChatModel: "some-model",
	}); err == nil {
		t.Fatal("expected plain http to a non-loopback host to be refused")
	}
	if got := credentials.APIKey("sketchy"); got != "" {
		t.Fatalf("stored API key = %q, want nothing saved for a rejected URL", got)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("providers = %v, want no entry for a rejected URL", cfg.Providers)
	}
	if _, built := loop.providers["sketchy"]; built {
		t.Fatal("rejected URL must not build a provider")
	}
}

// P-03: startup and /connect used to build adapters independently. Startup
// applied the key from the credential store; /connect applied only the key just
// typed. So re-running /connect on a saved provider to change its model — with
// the key field left blank, because it is already stored — replaced a working
// adapter with one that sent no Authorization header at all, until a restart.
func TestConnectKeepsTheStoredAPIKeyWhenNoneIsTyped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	authorization := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case authorization <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"m2"}]}`)
	}))
	defer server.Close()

	credentials, err := config.LoadCredentialStore()
	if err != nil {
		t.Fatalf("load credential store: %v", err)
	}
	cfg := &config.Config{}
	if err := cfg.SaveTo(filepath.Join(home, "config.yaml")); err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	loop := &Loop{providers: map[string]ai.ChatProvider{}, config: cfg, credentials: credentials}

	connect := func(model, apiKey string) {
		t.Helper()
		if _, err := loop.Connect(ConnectionInput{
			Name: "Acme", Type: "openai_compatible", BaseURL: server.URL, ChatModel: model, APIKey: apiKey,
		}); err != nil {
			t.Fatalf("connect with model %q: %v", model, err)
		}
	}
	connect("m1", "sk-stored")
	// Second pass: only the model changes. The key stays blank, the way a user
	// picking a different model would leave it.
	connect("m2", "")

	if _, err := loop.providers["acme"].ChatModels(context.Background()); err != nil {
		t.Fatalf("list models: %v", err)
	}
	select {
	case got := <-authorization:
		if got != "Bearer sk-stored" {
			t.Fatalf("Authorization = %q, want the stored key to survive a keyless reconnect", got)
		}
	default:
		t.Fatal("provider made no request")
	}
}

// P-03 acceptance: "Restore built-in Ollama" resets the built-in provider's own
// settings. It must not evict the other providers from config, or switching
// back to one of them would cost another login.
func TestRestoreOllamaKeepsOtherProviderEntries(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "acme",
		Providers:      []config.ProviderConfig{{Name: "Acme", Type: "openai_compatible", BaseURL: "https://api.acme.test/v1", ChatModel: "m1"}},
	}
	if err := cfg.SaveTo(filepath.Join(t.TempDir(), "config.yaml")); err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	loop := &Loop{providers: map[string]ai.ChatProvider{"ollama": ai.NewClient("http://localhost:11434", "qwen3:1.7b", "embed")}, config: cfg}

	if _, err := loop.restoreOllama(); err != nil {
		t.Fatalf("restore ollama: %v", err)
	}
	if cfg.ActiveProvider != "ollama" {
		t.Fatalf("active provider = %q, want ollama", cfg.ActiveProvider)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "Acme" {
		t.Fatalf("providers = %v, want the Acme entry kept", cfg.Providers)
	}
}

// A-04: one slug rule. cmd/athena registered providers under its own copy of
// this function; a provider registered under a slug this package cannot
// reproduce is unreachable at runtime.
func TestProviderIDSlug(t *testing.T) {
	for name, want := range map[string]string{
		"OpenAI Codex": "openai-codex",
		"xAI OAuth":    "xai-oauth",
		"  Acme  ":     "acme",
		"LM Studio!":   "lm-studio",
		"---":          "",
	} {
		if got := ProviderID(name); got != want {
			t.Errorf("ProviderID(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSavedModelPrefersConfiguredChoice(t *testing.T) {
	loop := &Loop{config: &config.Config{Providers: []config.ProviderConfig{{Name: "xAI OAuth", ChatModel: "grok-4-fast"}}}}
	if got := loop.savedModel(xaiOAuthProviderID, defaultXAIModel); got != "grok-4-fast" {
		t.Fatalf("savedModel = %q, want the user's configured grok-4-fast", got)
	}
	if got := loop.savedModel(codexProviderID, defaultCodexModel); got != defaultCodexModel {
		t.Fatalf("savedModel = %q, want the %q fallback", got, defaultCodexModel)
	}
}

// A-06 guarded /connect, but startup builds every saved provider entry and
// attaches the stored API key. config.yaml is an ordinary file: a hand-edited,
// mistyped, or synced entry must not be able to send that key to an arbitrary
// host on the first request after launch.
func TestBuildProviderRefusesAnUnsafeSavedBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	credentials, err := config.LoadCredentialStore()
	if err != nil {
		t.Fatalf("load credential store: %v", err)
	}
	creds := ProviderCredentials{APIKeys: credentials}

	for _, unsafe := range []string{
		"http://api.example.com/v1",   // cleartext key over the network
		"http://192.168.1.50:1234/v1", // LAN is not loopback
		"http://evil.com#@localhost",  // real host is evil.com
		"ftp://example.com/v1",
	} {
		entry := config.ProviderConfig{Name: "Saved", Type: "openai_compatible", BaseURL: unsafe, ChatModel: "m"}
		if _, err := BuildProvider(entry, creds); err == nil {
			t.Errorf("BuildProvider accepted saved base URL %q; the stored API key would be sent there", unsafe)
		}
	}

	for _, safe := range []string{"https://api.openai.com/v1", "http://localhost:11434/v1"} {
		entry := config.ProviderConfig{Name: "Saved", Type: "openai_compatible", BaseURL: safe, ChatModel: "m"}
		if _, err := BuildProvider(entry, creds); err != nil {
			t.Errorf("BuildProvider rejected safe base URL %q: %v", safe, err)
		}
	}

	// A provider with no base URL of its own (Codex uses fixed endpoints) must
	// still build.
	if _, err := BuildProvider(config.ProviderConfig{Name: "OpenAI Codex", Type: "openai_codex", ChatModel: "gpt-5.4"},
		ProviderCredentials{APIKeys: credentials, Codex: &ai.CodexOAuth{}}); err != nil {
		t.Errorf("BuildProvider rejected a provider that has no base URL: %v", err)
	}
}
