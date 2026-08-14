package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesAthenaPathsForFreshInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VaultPath != filepath.Join(home, "Athena") {
		t.Fatalf("vault path = %q", cfg.VaultPath)
	}
	if cfg.DBPath != filepath.Join(home, ".local", "share", "athena", "athena.db") {
		t.Fatalf("database path = %q", cfg.DBPath)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "athena", "config.yaml")); err != nil {
		t.Fatalf("Athena config was not created: %v", err)
	}
}

func TestLoadCopiesLegacyConfigAndPreservesConfiguredDataPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyPath := filepath.Join(home, ".config", "second-brain", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacyVault := filepath.Join(home, "SecondBrain")
	legacyDB := filepath.Join(home, ".local", "share", "second-brain", "second-brain.db")
	raw := []byte("vault_path: " + legacyVault + "\ndb_path: " + legacyDB + "\nollama_host: http://localhost:11434\nchat_model: legacy-chat\nembed_model: legacy-embed\n")
	if err := os.WriteFile(legacyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VaultPath != legacyVault || cfg.DBPath != legacyDB {
		t.Fatalf("legacy data paths changed: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "athena", "config.yaml")); err != nil {
		t.Fatalf("migrated config missing: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy recovery config missing: %v", err)
	}
}

func TestLoadCredentialStoreCopiesLegacyCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyPath := filepath.Join(home, ".config", "second-brain", "provider-credentials.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"api_keys":{"openai":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := LoadCredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.APIKey("openai") != "secret" {
		t.Fatal("legacy credential was not loaded")
	}
	want := filepath.Join(home, ".config", "athena", "provider-credentials.json")
	if store.path != want {
		t.Fatalf("credential path = %q, want %q", store.path, want)
	}
}

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
