package config

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateHome points every configuration lookup at a temporary directory.
// Every test that calls Load or LoadCredentialStore must use it: a test that
// ran against the real environment once overwrote a developer's live
// ~/.config/athena/config.yaml.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	return home
}

func TestLoadCreatesAthenaPathsForFreshInstall(t *testing.T) {
	home := isolateHome(t)

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
	home := isolateHome(t)
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
	home := isolateHome(t)
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

func TestLoadRejectsConfigMissingADataPath(t *testing.T) {
	for _, testCase := range []struct{ name, yaml string }{
		{"vault_path", "db_path: /tmp/athena.db\n"},
		{"db_path", "vault_path: /tmp/vault\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := isolateHome(t)
			path := filepath.Join(home, ".config", "athena", "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(testCase.yaml), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := Load()
			if err == nil {
				t.Fatalf("expected Load to reject a config with no %s", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.name) || !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %q, want it to name both %s and %s", err, testCase.name, path)
			}
		})
	}
}

func TestLoadHonorsXDGDirectories(t *testing.T) {
	isolateHome(t)
	configHome, dataHome := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPath != filepath.Join(dataHome, "athena", "athena.db") {
		t.Fatalf("database path = %q, want it under XDG_DATA_HOME", cfg.DBPath)
	}
	if _, err := os.Stat(filepath.Join(configHome, "athena", "config.yaml")); err != nil {
		t.Fatalf("config was not written under XDG_CONFIG_HOME: %v", err)
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

func TestLoadCredentialStoreTightensAndReportsAWorldReadableFile(t *testing.T) {
	home := isolateHome(t)
	path := filepath.Join(home, ".config", "athena", "provider-credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"api_keys":{"openai":"secret"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store, warning := loadCredentialStoreCapturingStderr(t)
	if store.APIKey("openai") != "secret" {
		t.Fatal("an over-permissive file should still load; refusing would lock the user out")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want the file tightened to 600", info.Mode().Perm())
	}
	if !strings.Contains(warning, path) || !strings.Contains(warning, "rotate") {
		t.Fatalf("warning = %q, want it to name the file and ask for a rotation", warning)
	}
}

func loadCredentialStoreCapturingStderr(t *testing.T) (*CredentialStore, string) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = write
	store, loadErr := LoadCredentialStore()
	os.Stderr = original
	write.Close()
	warning, readErr := io.ReadAll(read)
	read.Close()
	if loadErr != nil {
		t.Fatalf("load credential store: %v", loadErr)
	}
	if readErr != nil {
		t.Fatalf("read captured stderr: %v", readErr)
	}
	return store, string(warning)
}

func TestDeleteAPIKeyRemovesTheStoredSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := &CredentialStore{path: path, APIKeys: map[string]string{"openai": "secret-key", "xai": "keep-me"}}
	if err := store.saveLocked(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAPIKey("openai"); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if store.APIKey("openai") != "" {
		t.Fatal("deleted key is still in memory")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-key") {
		t.Fatalf("deleted key is still on disk: %s", raw)
	}
	if !containsCredential(raw, "xai", "keep-me") {
		t.Fatalf("unrelated key was dropped: %s", raw)
	}
	if err := store.DeleteAPIKey("openai"); err != nil {
		t.Fatalf("deleting an absent key should succeed: %v", err)
	}
}

func containsCredential(raw []byte, provider, key string) bool {
	var store CredentialStore
	return json.Unmarshal(raw, &store) == nil && store.APIKeys[provider] == key
}

// A Config built in code has no file behind it. Save must refuse rather than
// fall back to the user's real config path — that fallback let a test fixture
// overwrite a live configuration.
func TestSaveRefusesConfigThatWasNeverLoaded(t *testing.T) {
	cfg := &Config{VaultPath: "/tmp/vault", ChatModel: "test"}
	if err := cfg.Save(); err == nil {
		t.Fatal("expected Save to refuse a config with no source file")
	}
}

func TestSaveToRemembersItsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &Config{VaultPath: "/tmp/vault", ChatModel: "first"}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("save to %s: %v", path, err)
	}
	cfg.ChatModel = "second"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save after SaveTo: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(raw), "second") {
		t.Fatalf("saved config = %s, want the updated chat model", raw)
	}
}
