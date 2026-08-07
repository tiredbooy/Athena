package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// oauthSeconds accepts the number-or-string variants used by provider OAuth
// servers. Invalid and non-positive values become zero so each flow can apply
// its conservative fallback instead of accidentally busy-looping.
type oauthSeconds int64

func (s *oauthSeconds) UnmarshalJSON(raw []byte) error {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		*s = 0
		return nil
	}
	*s = oauthSeconds(seconds)
	return nil
}

// writeOwnerOnlyJSON atomically replaces an OAuth credential file. Writing a
// temporary file first matters for rotating refresh tokens: a crash must not
// leave half a JSON document and permanently strand the user's login.
func writeOwnerOnlyJSON(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure credential directory: %w", err)
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".athena-credentials-*")
	if err != nil {
		return fmt.Errorf("create temporary credential file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary credential file: %w", err)
	}
	if _, err := temporary.Write(append(raw, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary credential file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary credential file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary credential file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	return os.Chmod(path, 0o600)
}
