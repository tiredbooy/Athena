package notes

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// SyncFolderGraph creates one Athena-managed index note per real folder. The
// index links only to its direct children and direct notes, yielding a clean
// Obsidian graph hierarchy rather than a dense web of parent links.
func (s *Service) SyncFolderGraph() error {
	folders, err := utils.ListFolders(s.vaultPath)
	if err != nil {
		return fmt.Errorf("list folders for graph: %w", err)
	}
	folders = visibleFolders(folders)
	if err := s.removeStaleFolderIndexes(); err != nil {
		return err
	}

	children := make(map[string][]string, len(folders))
	for _, folder := range folders {
		parent := path.Dir(folder)
		if parent == "." {
			parent = ""
		}
		children[parent] = append(children[parent], folder)
	}
	for _, entries := range children {
		sort.Strings(entries)
	}

	notes, err := s.noteStore.All()
	if err != nil {
		return fmt.Errorf("list notes for graph: %w", err)
	}
	notesByFolder := make(map[string][]*models.Note)
	for _, note := range notes {
		rel := utils.RelVault(s.vaultPath, note.Path)
		if isManagedFolder(rel) {
			continue
		}
		folder := path.Dir(rel)
		if folder == "." {
			folder = ""
		}
		notesByFolder[folder] = append(notesByFolder[folder], note)
	}
	for _, entries := range notesByFolder {
		sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Title) < strings.ToLower(entries[j].Title) })
	}

	for _, folder := range folders {
		linkedFolders, err := s.readFolderLinks(folder, folders)
		if err != nil {
			return err
		}
		content, err := renderFolderIndex(folder, children[folder], linkedFolders, notesByFolder[folder], s.vaultPath)
		if err != nil {
			return err
		}
		if err := s.writeFolderIndex(folder, content); err != nil {
			return err
		}
	}
	return nil
}

func visibleFolders(folders []string) []string {
	visible := make([]string, 0, len(folders))
	for _, folder := range folders {
		if isManagedFolder(folder) {
			continue
		}
		visible = append(visible, folder)
	}
	return visible
}

func isManagedFolder(value string) bool {
	return value == ".trash" || strings.HasPrefix(value, ".trash/") || value == ".obsidian" || strings.HasPrefix(value, ".obsidian/") || value == "archive" || strings.HasPrefix(value, "archive/")
}

func renderFolderIndex(folder string, childFolders, linkedFolders []string, notes []*models.Note, vaultPath string) (string, error) {
	name := folderName(folder)
	var body strings.Builder
	body.WriteString("# ")
	body.WriteString(name)
	body.WriteString("\n")
	if parent := path.Dir(folder); parent != "." {
		body.WriteString("\n## Parent\n- ")
		body.WriteString(wikiLink(parent, folderName(parent)))
		body.WriteString("\n")
	}
	if len(childFolders) > 0 {
		body.WriteString("\n## Categories\n")
		for _, child := range childFolders {
			body.WriteString("- ")
			body.WriteString(wikiLink(child, folderName(child)))
			body.WriteString("\n")
		}
	}
	if len(linkedFolders) > 0 {
		body.WriteString("\n## Related folders\n")
		for _, linked := range linkedFolders {
			body.WriteString("- ")
			body.WriteString(wikiLink(linked, folderName(linked)))
			body.WriteString("\n")
		}
	}
	if len(notes) > 0 {
		body.WriteString("\n## Notes\n")
		for _, note := range notes {
			rel := strings.TrimSuffix(utils.RelVault(vaultPath, note.Path), ".md")
			body.WriteString("- ")
			body.WriteString(wikiLink(rel, note.Title))
			body.WriteString("\n")
		}
	}
	body.WriteString("\n<!-- Managed by Athena: folder graph index -->\n")
	return parser.RenderMarkdown(parser.Frontmatter{Title: name, AthenaIndex: true, LinkedFolders: linkedFolders}, strings.TrimSpace(body.String()))
}

func (s *Service) readFolderLinks(folder string, knownFolders []string) ([]string, error) {
	raw, err := utils.ReadNoteFile(filepath.Join(s.vaultPath, filepath.FromSlash(folder)+".md"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read folder index %s: %w", folder, err)
	}
	fm, _, err := parser.ParseMarkdown(raw)
	if err != nil {
		return nil, fmt.Errorf("parse folder index %s: %w", folder, err)
	}
	if !fm.AthenaIndex {
		return nil, fmt.Errorf("cannot create folder graph index for %q: %s is an existing user note", folder, utils.RelVault(s.vaultPath, filepath.Join(s.vaultPath, filepath.FromSlash(folder)+".md")))
	}

	known := make(map[string]bool, len(knownFolders))
	for _, knownFolder := range knownFolders {
		known[knownFolder] = true
	}
	unique := make(map[string]bool, len(fm.LinkedFolders))
	for _, linked := range fm.LinkedFolders {
		if linked != folder && known[linked] {
			unique[linked] = true
		}
	}
	links := make([]string, 0, len(unique))
	for linked := range unique {
		links = append(links, linked)
	}
	sort.Strings(links)
	return links, nil
}

func wikiLink(target, label string) string {
	return "[[" + target + "|" + label + "]]"
}

func folderName(folder string) string {
	name := path.Base(folder)
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	return strings.Title(name)
}

func (s *Service) writeFolderIndex(folder, content string) error {
	indexPath := filepath.Join(s.vaultPath, filepath.FromSlash(folder)+".md")
	raw, err := utils.ReadNoteFile(indexPath)
	if err == nil {
		frontmatter, _, parseErr := parser.ParseMarkdown(raw)
		if parseErr != nil {
			return fmt.Errorf("parse existing folder index %s: %w", folder, parseErr)
		}
		if !frontmatter.AthenaIndex {
			return fmt.Errorf("cannot create folder graph index for %q: %s is an existing user note", folder, utils.RelVault(s.vaultPath, indexPath))
		}
		if raw == content {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read folder index %s: %w", folder, err)
	}
	if err := utils.OverwriteNoteFile(indexPath, content); err != nil {
		return fmt.Errorf("write folder index %s: %w", folder, err)
	}
	return nil
}

func (s *Service) removeStaleFolderIndexes() error {
	return filepath.WalkDir(s.vaultPath, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(filePath) != ".md" {
			return nil
		}
		rel := utils.RelVault(s.vaultPath, filePath)
		if isManagedFolder(rel) {
			return nil
		}
		raw, err := utils.ReadNoteFile(filePath)
		if err != nil {
			return err
		}
		frontmatter, _, err := parser.ParseMarkdown(raw)
		if err != nil || !frontmatter.AthenaIndex {
			return err
		}
		folder := strings.TrimSuffix(rel, ".md")
		exists, err := utils.FolderExists(s.vaultPath, folder)
		if err != nil {
			return err
		}
		if !exists {
			return os.Remove(filePath)
		}
		return nil
	})
}
