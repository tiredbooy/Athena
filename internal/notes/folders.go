package notes

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// CreateFolder makes a single vault-relative folder (and any missing
// parents), e.g. "books/to-read".
func (s *Service) CreateFolder(folder string) (string, error) {
	clean, err := utils.CleanFolder(folder)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", fmt.Errorf("folder name is required")
	}
	if err := utils.EnsureDir(s.vaultPath, clean); err != nil {
		return "", fmt.Errorf("create folder: %w", err)
	}
	return clean, nil
}

// ListFolders returns every folder in the vault, vault-relative.
func (s *Service) ListFolders() ([]string, error) {
	return utils.ListFolders(s.vaultPath)
}

// FolderExists reports whether folder exists under the vault.
func (s *Service) FolderExists(folder string) (bool, error) {
	return utils.FolderExists(s.vaultPath, folder)
}

// LinkFolders records a bidirectional Obsidian graph connection between the
// supplied folders. Folder index notes own this metadata because they are the
// graph nodes Athena generates for real directories.
func (s *Service) LinkFolders(folders []string) ([]string, error) {
	unique := make(map[string]bool, len(folders))
	for _, folder := range folders {
		clean, err := utils.CleanFolder(folder)
		if err != nil {
			return nil, err
		}
		if clean == "" || isManagedFolder(clean) {
			return nil, fmt.Errorf("invalid folder %q", folder)
		}
		exists, err := utils.FolderExists(s.vaultPath, clean)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("folder %q not found", clean)
		}
		unique[clean] = true
	}
	if len(unique) < 2 {
		return nil, fmt.Errorf("at least two distinct folders are required")
	}

	linked := make([]string, 0, len(unique))
	for folder := range unique {
		linked = append(linked, folder)
	}
	sort.Strings(linked)
	if err := s.SyncFolderGraph(); err != nil {
		return nil, err
	}
	for _, folder := range linked {
		if err := s.addFolderLinks(folder, linked); err != nil {
			return nil, err
		}
	}
	if err := s.SyncFolderGraph(); err != nil {
		return nil, err
	}
	return linked, nil
}

func (s *Service) addFolderLinks(folder string, related []string) error {
	indexPath := filepath.Join(s.vaultPath, filepath.FromSlash(folder)+".md")
	raw, err := utils.ReadNoteFile(indexPath)
	if err != nil {
		return fmt.Errorf("read folder index %s: %w", folder, err)
	}
	fm, body, err := parser.ParseMarkdown(raw)
	if err != nil {
		return fmt.Errorf("parse folder index %s: %w", folder, err)
	}
	if !fm.AthenaIndex {
		return fmt.Errorf("folder index %q is not Athena-managed", folder)
	}
	links := make(map[string]bool, len(fm.LinkedFolders)+len(related))
	for _, linked := range fm.LinkedFolders {
		links[linked] = true
	}
	for _, relatedFolder := range related {
		if relatedFolder != folder {
			links[relatedFolder] = true
		}
	}
	fm.LinkedFolders = fm.LinkedFolders[:0]
	for linked := range links {
		fm.LinkedFolders = append(fm.LinkedFolders, linked)
	}
	sort.Strings(fm.LinkedFolders)
	content, err := parser.RenderMarkdown(fm, body)
	if err != nil {
		return fmt.Errorf("render folder index %s: %w", folder, err)
	}
	if err := utils.OverwriteNoteFile(indexPath, content); err != nil {
		return fmt.Errorf("write folder index %s: %w", folder, err)
	}
	return nil
}

// DeleteEmptyFolder removes folder if it contains no files or subfolders.
// The .trash tree is off-limits — it's managed automatically.
func (s *Service) DeleteEmptyFolder(folder string) error {
	clean, err := utils.CleanFolder(folder)
	if err != nil {
		return err
	}
	if clean == ".trash" || strings.HasPrefix(clean, ".trash/") {
		return fmt.Errorf("the trash folder is managed automatically and can't be deleted directly")
	}
	return utils.DeleteEmptyFolder(s.vaultPath, clean)
}

// RenameFolder changes a folder's own name, keeping it under the same
// parent. newName must be a single path segment, not a nested path.
func (s *Service) RenameFolder(oldFolder, newName string) (string, error) {
	newName = strings.Trim(strings.TrimSpace(newName), "/")
	if newName == "" || strings.Contains(newName, "/") {
		return "", fmt.Errorf("new name must be a single folder segment, not a path")
	}
	oldClean, err := utils.CleanFolder(oldFolder)
	if err != nil {
		return "", err
	}

	parent := ""
	if idx := strings.LastIndex(oldClean, "/"); idx >= 0 {
		parent = oldClean[:idx]
	}
	newFolder := newName
	if parent != "" {
		newFolder = parent + "/" + newName
	}
	return s.moveFolder(oldClean, newFolder)
}

// MoveFolder relocates a folder under a new parent, keeping its own name.
func (s *Service) MoveFolder(oldFolder, newParent string) (string, error) {
	oldClean, err := utils.CleanFolder(oldFolder)
	if err != nil {
		return "", err
	}
	name := oldClean
	if idx := strings.LastIndex(oldClean, "/"); idx >= 0 {
		name = oldClean[idx+1:]
	}
	newParentClean, err := utils.CleanFolder(newParent)
	if err != nil {
		return "", err
	}
	newFolder := name
	if newParentClean != "" {
		newFolder = newParentClean + "/" + name
	}
	return s.moveFolder(oldClean, newFolder)
}

// moveFolder is the shared implementation behind RenameFolder and
// MoveFolder: move the directory on disk, then repoint every note whose
// path was inside it so the DB never drifts from the filesystem.
func (s *Service) moveFolder(oldFolder, newFolder string) (string, error) {
	if oldFolder == "" {
		return "", fmt.Errorf("cannot move the vault root")
	}
	if newFolder == oldFolder || strings.HasPrefix(newFolder+"/", oldFolder+"/") {
		return "", fmt.Errorf("cannot move %q into itself", oldFolder)
	}

	oldAbs, newAbs, err := utils.MoveDir(s.vaultPath, oldFolder, newFolder)
	if err != nil {
		return "", err
	}

	all, err := s.noteStore.All()
	if err != nil {
		return "", fmt.Errorf("load notes after move: %w", err)
	}
	prefix := oldAbs + string(filepath.Separator)
	for _, n := range all {
		if n.Path == oldAbs || strings.HasPrefix(n.Path, prefix) {
			n.Path = newAbs + strings.TrimPrefix(n.Path, oldAbs)
			if err := s.noteStore.Update(n); err != nil {
				return "", fmt.Errorf("repoint note %d after folder move: %w", n.ID, err)
			}
		}
	}
	return newFolder, nil
}
