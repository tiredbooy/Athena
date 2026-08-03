package utils

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// CleanFolder normalizes a vault-relative folder path: slashes cleaned,
// no leading/trailing separators, no ".." segments (path escape guard).
func CleanFolder(folder string) (string, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "", nil
	}
	folder = filepath.ToSlash(folder)
	folder = strings.Trim(folder, "/")
	if folder == "" || folder == "." {
		return "", nil
	}
	for _, part := range strings.Split(folder, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid folder path %q", folder)
		}
	}
	return folder, nil
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

// MoveFile relocates a single file, creating the target's parent dirs and
// refusing to clobber an existing file at the destination.
func MoveFile(oldPath, newPath string) error {
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("target file already exists at %s", newPath)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return fmt.Errorf("prepare target folder: %w", err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("move file: %w", err)
	}
	return nil
}

// EnsureDir creates a directory (and parents) under vaultRoot.
// folder is vault-relative; empty means the vault root itself.
func EnsureDir(vaultRoot, folder string) error {
	clean, err := CleanFolder(folder)
	if err != nil {
		return err
	}
	dir := vaultRoot
	if clean != "" {
		dir = filepath.Join(vaultRoot, filepath.FromSlash(clean))
	}
	return os.MkdirAll(dir, 0o755)
}

// FolderExists reports whether a vault-relative folder exists on disk.
func FolderExists(vaultRoot, folder string) (bool, error) {
	clean, err := CleanFolder(folder)
	if err != nil {
		return false, err
	}
	dir := vaultRoot
	if clean != "" {
		dir = filepath.Join(vaultRoot, filepath.FromSlash(clean))
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check folder: %w", err)
	}
	return info.IsDir(), nil
}

// ListFolders returns every folder under vaultRoot, vault-relative and
// slash-separated, sorted. The .trash tree is skipped — it's an internal
// implementation detail, not something the agent should offer to browse.
func ListFolders(vaultRoot string) ([]string, error) {
	var folders []string
	err := filepath.WalkDir(vaultRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || path == vaultRoot {
			return nil
		}
		rel := RelVault(vaultRoot, path)
		if rel == ".trash" || strings.HasPrefix(rel, ".trash/") || rel == ".obsidian" || strings.HasPrefix(rel, ".obsidian/") {
			return filepath.SkipDir
		}
		folders = append(folders, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	sort.Strings(folders)
	return folders, nil
}

// DeleteEmptyFolder removes a vault-relative folder, refusing if it still
// contains files or subfolders — deliberately no recursive delete here.
func DeleteEmptyFolder(vaultRoot, folder string) error {
	clean, err := CleanFolder(folder)
	if err != nil {
		return err
	}
	if clean == "" {
		return fmt.Errorf("cannot delete the vault root")
	}
	dir := filepath.Join(vaultRoot, filepath.FromSlash(clean))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read folder: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("folder %q is not empty (%d items)", clean, len(entries))
	}
	if err := os.Remove(dir); err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	return nil
}

// MoveDir relocates a vault-relative folder to another vault-relative path,
// refusing to clobber an existing folder at the destination. Used for both
// "rename" (same parent, new name) and "move" (new parent) — those are the
// same physical operation, just different target paths. Returns the old
// and new absolute paths so callers can repoint any DB rows underneath.
func MoveDir(vaultRoot, oldFolder, newFolder string) (oldAbs, newAbs string, err error) {
	oldClean, err := CleanFolder(oldFolder)
	if err != nil {
		return "", "", err
	}
	newClean, err := CleanFolder(newFolder)
	if err != nil {
		return "", "", err
	}
	if oldClean == "" {
		return "", "", fmt.Errorf("cannot move the vault root")
	}
	if newClean == "" {
		return "", "", fmt.Errorf("cannot move a folder to the vault root")
	}

	oldAbs = filepath.Join(vaultRoot, filepath.FromSlash(oldClean))
	newAbs = filepath.Join(vaultRoot, filepath.FromSlash(newClean))

	if _, err := os.Stat(oldAbs); err != nil {
		return "", "", fmt.Errorf("source folder %q not found: %w", oldClean, err)
	}
	if _, err := os.Stat(newAbs); err == nil {
		return "", "", fmt.Errorf("target folder %q already exists", newClean)
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return "", "", fmt.Errorf("prepare target parent: %w", err)
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return "", "", fmt.Errorf("move folder: %w", err)
	}
	return oldAbs, newAbs, nil
}

// NotePath builds vaultRoot[/folder]/slug.md.
func NotePath(vaultRoot, folder, title string) (string, error) {
	clean, err := CleanFolder(folder)
	if err != nil {
		return "", err
	}
	slug := Slugify(title)
	if slug == "" {
		slug = "untitled"
	}
	if clean == "" {
		return filepath.Join(vaultRoot, slug+".md"), nil
	}
	return filepath.Join(vaultRoot, filepath.FromSlash(clean), slug+".md"), nil
}

// RelVault returns path relative to vaultRoot using slash separators,
// suitable for display and catalog entries.
func RelVault(vaultRoot, absPath string) string {
	rel, err := filepath.Rel(vaultRoot, absPath)
	if err != nil {
		return absPath
	}
	return filepath.ToSlash(rel)
}
