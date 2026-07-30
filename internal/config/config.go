package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	VaultPath  string `yaml:"vault_path"`
	DBPath     string `yaml:"db_path"`
	OllamaHost string `yaml:"ollama_host"`
	ChatModel  string `yaml:"chat_model"`
	EmbedModel string `yaml:"embed_model"`
}

func configFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "second-brain", "config.yaml"), nil
}

func defaultConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	dataDir := filepath.Join(home, ".local", "share", "second-brain")

	return &Config{
		VaultPath:  filepath.Join(home, "SecondBrain"),
		DBPath:     filepath.Join(dataDir, "second-brain.db"),
		OllamaHost: "http://localhost:11434",
		ChatModel:  "hf.co/mradermacher/Qwen3.5-9B-Claude-4.6-HighIQ-THINKING-HERETIC-UNCENSORED-GGUF:Q8_0",
		EmbedModel: "nomic-embed-text",
	}, nil
}

func Load() (*Config, error) {
	path, err := configFilePath()
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
	return &cfg, nil
}

func save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
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