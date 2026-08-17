package appdirs

import (
	"os"
	"path/filepath"
	"testing"
)

// isolateHome keeps a test off the developer's real configuration, including
// when their shell exports XDG overrides.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	return home
}

func TestPrepareConfigFileCopiesLegacyFileWithoutDeletingIt(t *testing.T) {
	home := isolateHome(t)
	legacy := filepath.Join(home, ".config", "second-brain", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("vault_path: /legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := PrepareConfigFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "athena", "config.yaml")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "vault_path: /legacy\n" {
		t.Fatalf("migrated contents = %q, err = %v", raw, err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy recovery copy was removed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("migrated permissions = %o", info.Mode().Perm())
	}
}

func TestPrepareConfigFilePrefersExistingAthenaFile(t *testing.T) {
	home := isolateHome(t)
	legacy := filepath.Join(home, ".config", "second-brain", "config.yaml")
	target := filepath.Join(home, ".config", "athena", "config.yaml")
	for _, path := range []string{legacy, target} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("athena"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := PrepareConfigFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "athena" {
		t.Fatalf("canonical contents = %q, err = %v", raw, err)
	}
}

func TestXDGOverridesTakePrecedenceOverHome(t *testing.T) {
	isolateHome(t)
	configHome, dataHome := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)

	config, err := ConfigFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if config != filepath.Join(configHome, "athena", "config.yaml") {
		t.Fatalf("config file = %q", config)
	}
	data, err := DataFile("athena.db")
	if err != nil {
		t.Fatal(err)
	}
	if data != filepath.Join(dataHome, "athena", "athena.db") {
		t.Fatalf("data file = %q", data)
	}
}

// The XDG specification says a relative base directory must be ignored;
// honouring one would resolve Athena's state against the working directory.
func TestRelativeXDGValueFallsBackToHome(t *testing.T) {
	home := isolateHome(t)
	t.Setenv("XDG_DATA_HOME", "relative/share")

	data, err := DataFile("athena.db")
	if err != nil {
		t.Fatal(err)
	}
	if data != filepath.Join(home, ".local", "share", "athena", "athena.db") {
		t.Fatalf("data file = %q, want the $HOME fallback", data)
	}
}

func TestPrepareConfigFileMigratesLegacyFileUnderXDGConfigHome(t *testing.T) {
	isolateHome(t)
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	legacy := filepath.Join(configHome, "second-brain", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("vault_path: /legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := PrepareConfigFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "vault_path: /legacy\n" {
		t.Fatalf("migrated contents = %q, err = %v", raw, err)
	}
}
