package config

import "testing"

func TestRestoreMissingOllamaDefaultsKeepsCustomValues(t *testing.T) {
	cfg := &Config{OllamaHost: "http://127.0.0.1:11435", ChatModel: "custom"}
	if !cfg.RestoreMissingOllamaDefaults() {
		t.Fatal("expected missing embedding model to be repaired")
	}
	if cfg.OllamaHost != "http://127.0.0.1:11435" || cfg.ChatModel != "custom" {
		t.Fatalf("custom values were changed: %+v", cfg)
	}
	if cfg.EmbedModel != DefaultEmbeddingModel {
		t.Fatalf("embedding model = %q", cfg.EmbedModel)
	}
}

func TestRestoreOllamaDefaultsResetsBuiltInConnection(t *testing.T) {
	cfg := &Config{OllamaHost: "broken", ChatModel: "broken", EmbedModel: "broken", ActiveProvider: "remote"}
	cfg.RestoreOllamaDefaults()
	if cfg.OllamaHost != DefaultOllamaHost || cfg.ChatModel != DefaultChatModel || cfg.EmbedModel != DefaultEmbeddingModel || cfg.ActiveProvider != "ollama" {
		t.Fatalf("defaults were not restored: %+v", cfg)
	}
}
