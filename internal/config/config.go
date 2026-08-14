package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	if cfg.RestoreMissingOllamaDefaults() {
		if err := save(path, &cfg); err != nil {
			return nil, fmt.Errorf("repair missing Ollama defaults: %w", err)
		}
	}
	return &cfg, nil
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
func (c *Config) Save() error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
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
