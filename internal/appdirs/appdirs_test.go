package appdirs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareConfigFileCopiesLegacyFileWithoutDeletingIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	home := t.TempDir()
	t.Setenv("HOME", home)
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
