// Package appdirs owns Athena's per-user filesystem layout.
package appdirs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	configDirectoryName       = "athena"
	legacyConfigDirectoryName = "second-brain"
)

// ConfigFile returns the canonical location for an Athena configuration file.
func ConfigFile(name string) (string, error) {
	root, err := xdgRoot("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return "", err
	}
	return filepath.Join(root, configDirectoryName, name), nil
}

// DataFile returns the canonical location for an Athena application-data file.
func DataFile(name string) (string, error) {
	root, err := xdgRoot("XDG_DATA_HOME", filepath.Join(".local", "share"))
	if err != nil {
		return "", err
	}
	return filepath.Join(root, configDirectoryName, name), nil
}

// xdgRoot resolves an XDG base directory. The specification requires the
// variable to hold an absolute path and says a relative one must be ignored,
// so anything else falls back to the documented location under $HOME instead
// of resolving against the working directory.
func xdgRoot(variable, homeFallback string) (string, error) {
	if root := os.Getenv(variable); filepath.IsAbs(root) {
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, homeFallback), nil
}

// PrepareConfigFile copies a file from the former second-brain config
// directory when the Athena path does not exist yet. The legacy copy is kept
// as a recovery fallback; after this call all reads and writes use Athena's
// canonical path.
func PrepareConfigFile(name string) (string, error) {
	target, err := ConfigFile(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(target); err == nil {
		return target, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect Athena config file: %w", err)
	}

	configRoot, err := xdgRoot("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return "", err
	}
	legacy := filepath.Join(configRoot, legacyConfigDirectoryName, name)
	legacyFile, err := os.Open(legacy)
	if os.IsNotExist(err) {
		return target, nil
	}
	if err != nil {
		return "", fmt.Errorf("open legacy config file: %w", err)
	}
	defer legacyFile.Close()

	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("create Athena config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("secure Athena config directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".athena-migration-*")
	if err != nil {
		return "", fmt.Errorf("create temporary migration file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", fmt.Errorf("secure temporary migration file: %w", err)
	}
	if _, err := io.Copy(temporary, legacyFile); err != nil {
		temporary.Close()
		return "", fmt.Errorf("copy legacy config file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", fmt.Errorf("sync migrated config file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close migrated config file: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return "", fmt.Errorf("install migrated config file: %w", err)
	}
	return target, nil
}
