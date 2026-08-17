package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tiredbooy/internal/appdirs"
)

// CredentialStore keeps provider secrets separate from the ordinary YAML
// configuration. The file is local-only and owner-readable/writable.
type CredentialStore struct {
	mu      sync.RWMutex
	path    string
	APIKeys map[string]string `json:"api_keys"`
}

func LoadCredentialStore() (*CredentialStore, error) {
	path, err := appdirs.PrepareConfigFile("provider-credentials.json")
	if err != nil {
		return nil, err
	}
	store := &CredentialStore{path: path, APIKeys: make(map[string]string)}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect provider credentials: %w", err)
	}
	if err := restrictCredentialsToOwner(path, info.Mode().Perm()); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
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

// DeleteAPIKey removes a stored key, so a leaked or rotated credential can be
// taken out of the file instead of being overwritten with a placeholder.
// Deleting a provider that has no stored key succeeds: the caller's intent —
// no key on disk for this provider — already holds.
func (s *CredentialStore) DeleteAPIKey(providerID string) error {
	if s == nil {
		return fmt.Errorf("provider credential store is unavailable")
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return fmt.Errorf("provider is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.APIKeys[providerID]
	if !existed {
		return nil
	}
	delete(s.APIKeys, providerID)
	if err := s.saveLocked(); err != nil {
		s.APIKeys[providerID] = previous
		return err
	}
	return nil
}

// restrictCredentialsToOwner repairs a credential file that anyone but its
// owner can read. Athena warns and tightens the mode rather than refusing to
// load: the key is already exposed, so failing shut protects nothing, while it
// would lock the user out of every configured provider with no in-app way to
// fix the permissions. The warning goes to stderr because it asks for a human
// decision — rotating the key — that Athena cannot make.
func restrictCredentialsToOwner(path string, mode os.FileMode) error {
	if mode&0o077 == 0 {
		return nil
	}
	fmt.Fprintf(os.Stderr, "athena: %s had mode %04o (readable outside its owner); tightening to 0600 — rotate any API key it holds\n", path, mode)
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure provider credentials: %w", err)
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
	return appdirs.ConfigFile("provider-credentials.json")
}
