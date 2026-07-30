package utils

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a note title into a safe filename, e.g.
// "Go: Nil Slices & JSON" -> "go-nil-slices-json"
func Slugify(title string) string {
	s := strings.ToLower(title)
	s = nonSlugChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// WriteNoteFile writes content to path, creating any missing parent dirs.
// Fails loudly if the file already exists — we never want to silently
// overwrite a note the user wrote by hand.
func WriteNoteFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return os.ErrExist
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ReadNoteFile is a thin wrapper for symmetry/readability at call sites.
func ReadNoteFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// OverwriteNoteFile replaces an existing note file's content, used by
// updates where clobbering is intentional (unlike WriteNoteFile for create).
func OverwriteNoteFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
