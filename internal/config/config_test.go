package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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

func TestCredentialStorePersistsAPIKeysWithOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := &CredentialStore{path: path, APIKeys: make(map[string]string)}
	if err := store.SaveAPIKey("xai", "secret-key"); err != nil {
		t.Fatalf("save key: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credentials: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want 600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil || !containsCredential(raw, "xai", "secret-key") {
		t.Fatalf("saved credentials are missing: %s, err=%v", raw, err)
	}
}

func containsCredential(raw []byte, provider, key string) bool {
	var store CredentialStore
	return json.Unmarshal(raw, &store) == nil && store.APIKeys[provider] == key
}
