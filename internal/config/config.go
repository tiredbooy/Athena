package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiredbooy/internal/appdirs"
	"gopkg.in/yaml.v3"
)

const (
	DefaultOllamaHost     = "http://localhost:11434"
	DefaultChatModel      = "hf.co/mradermacher/Qwen3.5-9B-Claude-4.6-HighIQ-THINKING-HERETIC-UNCENSORED-GGUF:Q8_0"
	DefaultEmbeddingModel = "qwen3-embedding:0.6b"
)

type Config struct {
	VaultPath         string                  `yaml:"vault_path"`
	DBPath            string                  `yaml:"db_path"`
	OllamaHost        string                  `yaml:"ollama_host"`
	ChatModel         string                  `yaml:"chat_model"`
	EmbedModel        string                  `yaml:"embed_model"`
	EmbeddingProvider EmbeddingProviderConfig `yaml:"embedding_provider,omitempty"`
	// Providers contains chat-only connections. Embeddings intentionally remain
	// on the local Ollama configuration above until a separate embedding
	// migration is implemented.
	Providers      []ProviderConfig `yaml:"providers,omitempty"`
	ActiveProvider string           `yaml:"active_provider,omitempty"`

	// path records where this config was loaded from. Save writes back to it
	// rather than re-deriving the user's config path, so a Config built in
	// code — a test fixture, say — cannot overwrite the real file by default.
	path string `yaml:"-"`
}
type EmbeddingProviderConfig struct {
	Type      string `yaml:"type"`
	Name      string `yaml:"name"`
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
	Model     string `yaml:"model"`
}

// ProviderConfig is safe to keep in the regular config file: it contains an
// environment-variable name, never the credential itself.
type ProviderConfig struct {
	Name      string `yaml:"name"`
	Type      string `yaml:"type"` // "openai" or "openai_compatible"
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
	ChatModel string `yaml:"chat_model"`
}

func configFilePath() (string, error) {
	return appdirs.ConfigFile("config.yaml")
}

func defaultConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	dbPath, err := appdirs.DataFile("athena.db")
	if err != nil {
		return nil, err
	}

	return &Config{
		VaultPath:  filepath.Join(home, "Athena"),
		DBPath:     dbPath,
		OllamaHost: DefaultOllamaHost,
		ChatModel:  DefaultChatModel,
		EmbedModel: DefaultEmbeddingModel,
	}, nil
}

func Load() (*Config, error) {
	path, err := appdirs.PrepareConfigFile("config.yaml")
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg, err := defaultConfig()
		if err != nil {
			return nil, err
		}
		cfg.path = path
		if err := save(path, cfg); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
		return cfg, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.path = path
	if err := cfg.validateDataPaths(path); err != nil {
		return nil, err
	}
	if cfg.RestoreMissingOllamaDefaults() {
		if err := save(path, &cfg); err != nil {
			return nil, fmt.Errorf("repair missing Ollama defaults: %w", err)
		}
	}
	return &cfg, nil
}

// validateDataPaths refuses a config that does not say where the user's notes
// and database live. Unlike the Ollama fields these are not repaired to
// defaults: an empty path is joined against the process working directory, so
// repairing it silently would either create a second empty vault somewhere
// surprising or point Athena at a different database than the one holding the
// user's notes. Failing names the file and field so the user can fix it.
func (c *Config) validateDataPaths(path string) error {
	for _, field := range []struct{ name, value string }{
		{"vault_path", c.VaultPath},
		{"db_path", c.DBPath},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("config %s: %s is empty; set it to the path Athena should use", path, field.name)
		}
	}
	return nil
}

// RestoreMissingOllamaDefaults repairs incomplete configuration without
// replacing a deliberate custom local endpoint or model choice.
func (c *Config) RestoreMissingOllamaDefaults() bool {
	changed := false
	if c.OllamaHost == "" {
		c.OllamaHost, changed = DefaultOllamaHost, true
	}
	if c.ChatModel == "" {
		c.ChatModel, changed = DefaultChatModel, true
	}
	if c.EmbedModel == "" {
		c.EmbedModel, changed = DefaultEmbeddingModel, true
	}
	return changed
}

// RestoreOllamaDefaults deliberately returns the built-in provider to its
// shipped values after a bad manual edit or unwanted custom connection.
func (c *Config) RestoreOllamaDefaults() {
	c.OllamaHost, c.ChatModel, c.EmbedModel = DefaultOllamaHost, DefaultChatModel, DefaultEmbeddingModel
	c.ActiveProvider = "ollama"
}

func save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// Save writes the config back to the standard config path (e.g. after
// the user switches chat models with /model).
// Save writes the config back to the file it was loaded from. A Config that
// was never loaded has no path, and saving it is refused: silently falling
// back to the user's real config file turned an in-memory fixture into a
// destructive write.
func (c *Config) Save() error {
	if c.path == "" {
		return fmt.Errorf("config was not loaded from a file; nothing to save")
	}
	return save(c.path, c)
}

// SaveTo writes the config to an explicit path and remembers it, so later
// Save calls target the same file. Callers that build a config in code use
// this instead of Save.
func (c *Config) SaveTo(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path cannot be empty")
	}
	c.path = path
	return save(path, c)
}

func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.VaultPath, 0o755); err != nil {
		return fmt.Errorf("create vault dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.DBPath), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	return nil
}
