package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// CredentialStore keeps provider secrets separate from the ordinary YAML
// configuration. The file is local-only and owner-readable/writable.
type CredentialStore struct {
	mu      sync.RWMutex
	path    string
	APIKeys map[string]string `json:"api_keys"`
}

func LoadCredentialStore() (*CredentialStore, error) {
	path, err := credentialFilePath()
	if err != nil {
		return nil, err
	}
	store := &CredentialStore{path: path, APIKeys: make(map[string]string)}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read provider credentials: %w", err)
	}
	if err := json.Unmarshal(raw, store); err != nil {
		return nil, fmt.Errorf("parse provider credentials: %w", err)
	}
	if store.APIKeys == nil {
		store.APIKeys = make(map[string]string)
	}
	return store, nil
}

func (s *CredentialStore) APIKey(providerID string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.APIKeys[strings.TrimSpace(providerID)]
}

func (s *CredentialStore) SaveAPIKey(providerID, key string) error {
	if s == nil {
		return fmt.Errorf("provider credential store is unavailable")
	}
	providerID, key = strings.TrimSpace(providerID), strings.TrimSpace(key)
	if providerID == "" || key == "" {
		return fmt.Errorf("provider and API key are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.APIKeys[providerID]
	s.APIKeys[providerID] = key
	if err := s.saveLocked(); err != nil {
		if existed {
			s.APIKeys[providerID] = previous
		} else {
			delete(s.APIKeys, providerID)
		}
		return err
	}
	return nil
}

func (s *CredentialStore) saveLocked() error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create provider credentials directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure provider credentials directory: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode provider credentials: %w", err)
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(directory, ".athena-provider-credentials-*")
	if err != nil {
		return fmt.Errorf("create temporary provider credentials: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary provider credentials: %w", err)
	}
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary provider credentials: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary provider credentials: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary provider credentials: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace provider credentials: %w", err)
	}
	return os.Chmod(s.path, 0o600)
}

func credentialFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "second-brain", "provider-credentials.json"), nil
}
