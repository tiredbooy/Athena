package notes

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
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
	if err := s.syncTopLevelFolderColors(folders); err != nil {
		return err
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
		body.WriteString("\n## Subfolders\n")
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

// syncTopLevelFolderColors adds stable Obsidian graph color groups for the
// generated index note of each top-level folder. It only appends missing
// groups, so existing Obsidian settings and user-defined color groups remain
// untouched. A query targets "work.md", not the whole work subtree, so the
// colored orb represents the folder node itself.
func (s *Service) syncTopLevelFolderColors(folders []string) error {
	topLevel := make([]string, 0, len(folders))
	for _, folder := range folders {
		if path.Dir(folder) != "." {
			continue
		}
		topLevel = append(topLevel, folder)
	}
	return s.syncFolderColors(topLevel)
}

// AddFolderGraphColors gives one real folder and, optionally, only its direct
// children stable colors in Obsidian's graph. It never overwrites an existing
// color group, including groups the user created themselves.
func (s *Service) AddFolderGraphColors(folder string, includeChildren bool) ([]string, error) {
	clean, err := utils.CleanFolder(folder)
	if err != nil {
		return nil, err
	}
	if clean == "" || isManagedFolder(clean) {
		return nil, fmt.Errorf("invalid folder %q", folder)
	}
	exists, err := utils.FolderExists(s.vaultPath, clean)
	if err != nil {
		return nil, fmt.Errorf("check folder: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("folder %q does not exist", clean)
	}
	if err := s.SyncFolderGraph(); err != nil {
		return nil, err
	}

	colored, err := s.folderColorTargets(clean, includeChildren)
	if err != nil {
		return nil, err
	}
	if err := s.syncFolderColors(colored); err != nil {
		return nil, err
	}
	return colored, nil
}

// VerifyFolderGraphColors confirms that the requested folder index nodes have
// durable color groups. It keeps verification of Obsidian settings inside the
// notes domain instead of making the application layer parse graph.json.
func (s *Service) VerifyFolderGraphColors(folder string, includeChildren bool) error {
	clean, err := utils.CleanFolder(folder)
	if err != nil {
		return err
	}
	if clean == "" || isManagedFolder(clean) {
		return fmt.Errorf("invalid folder %q", folder)
	}
	targets, err := s.folderColorTargets(clean, includeChildren)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(filepath.Join(s.vaultPath, ".obsidian", "graph.json"))
	if err != nil {
		return fmt.Errorf("read Obsidian graph settings: %w", err)
	}
	var settings struct {
		ColorGroups []struct {
			Query string          `json:"query"`
			Color json.RawMessage `json:"color"`
		} `json:"colorGroups"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("parse Obsidian graph settings: %w", err)
	}
	known := make(map[string]graphColor, len(settings.ColorGroups))
	for _, group := range settings.ColorGroups {
		var color graphColor
		if err := json.Unmarshal(group.Color, &color); err == nil && color.Valid() {
			known[group.Query] = color
		}
	}
	for _, target := range targets {
		query := graphPathQuery(target + ".md")
		if !known[query].Valid() {
			return fmt.Errorf("color group for folder %q is missing", target)
		}
	}
	return nil
}

// SetGraphNodeSizeMultiplier changes Obsidian's native, graph-wide node size.
// Per-folder node sizes are not supported by the core Graph view; that needs a
// graph plugin rather than an unreliable private-file convention.
func (s *Service) SetGraphNodeSizeMultiplier(multiplier float64) error {
	if !validGraphNodeSize(multiplier) {
		return fmt.Errorf("node size multiplier must be between 0.25 and 3")
	}
	settingsPath, raw, settings, err := s.loadGraphSettings()
	if err != nil {
		return err
	}
	settings["nodeSizeMultiplier"] = json.RawMessage(mustJSON(multiplier))
	return saveGraphSettings(settingsPath, raw, settings)
}

func (s *Service) VerifyGraphNodeSizeMultiplier(multiplier float64) error {
	if !validGraphNodeSize(multiplier) {
		return fmt.Errorf("node size multiplier must be between 0.25 and 3")
	}
	_, _, settings, err := s.loadGraphSettings()
	if err != nil {
		return err
	}
	var saved float64
	if err := json.Unmarshal(settings["nodeSizeMultiplier"], &saved); err != nil {
		return fmt.Errorf("read saved node size multiplier: %w", err)
	}
	if saved != multiplier {
		return fmt.Errorf("node size multiplier is %g, want %g", saved, multiplier)
	}
	return nil
}

func validGraphNodeSize(value float64) bool {
	return value >= 0.25 && value <= 3
}

func (s *Service) folderColorTargets(folder string, includeChildren bool) ([]string, error) {
	exists, err := utils.FolderExists(s.vaultPath, folder)
	if err != nil {
		return nil, fmt.Errorf("check folder: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("folder %q does not exist", folder)
	}

	targets := []string{folder}
	if includeChildren {
		folders, err := utils.ListFolders(s.vaultPath)
		if err != nil {
			return nil, fmt.Errorf("list child folders: %w", err)
		}
		for _, candidate := range visibleFolders(folders) {
			if path.Dir(candidate) == folder {
				targets = append(targets, candidate)
			}
		}
	}
	sort.Strings(targets)
	return targets, nil
}

func (s *Service) syncFolderColors(folders []string) error {
	queries := make(map[string]graphColor, len(folders))
	for _, folder := range folders {
		indexPath := folder + ".md"
		queries[graphPathQuery(indexPath)] = graphColorForFolder(folder)
	}
	if len(queries) == 0 {
		return nil
	}

	settingsPath, raw, settings, err := s.loadGraphSettings()
	if err != nil {
		return err
	}

	var groups []map[string]json.RawMessage
	if encoded, ok := settings["colorGroups"]; ok && len(encoded) > 0 && string(encoded) != "null" {
		if err := json.Unmarshal(encoded, &groups); err != nil {
			return fmt.Errorf("parse Obsidian graph color groups: %w", err)
		}
	}
	knownGroups := make(map[string]int, len(groups))
	for index, group := range groups {
		var query string
		if err := json.Unmarshal(group["query"], &query); err == nil {
			knownGroups[query] = index
		}
	}
	for query, color := range queries {
		index, exists := knownGroups[query]
		if exists && validGraphColor(groups[index]["color"]) {
			continue
		}
		if exists {
			// Athena previously wrote a CSS color string, which Obsidian rejects.
			// Repair only malformed settings; a valid user-selected color wins.
			groups[index]["color"] = json.RawMessage(mustJSON(color))
			continue
		}
		groups = append(groups, map[string]json.RawMessage{
			"query": json.RawMessage(mustJSON(query)),
			"color": json.RawMessage(mustJSON(color)),
		})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		var left, right string
		_ = json.Unmarshal(groups[i]["query"], &left)
		_ = json.Unmarshal(groups[j]["query"], &right)
		return left < right
	})
	encodedGroups, err := json.Marshal(groups)
	if err != nil {
		return fmt.Errorf("encode Obsidian graph color groups: %w", err)
	}
	settings["colorGroups"] = encodedGroups
	return saveGraphSettings(settingsPath, raw, settings)
}

func (s *Service) loadGraphSettings() (string, []byte, map[string]json.RawMessage, error) {
	settingsPath := filepath.Join(s.vaultPath, ".obsidian", "graph.json")
	raw, err := os.ReadFile(settingsPath)
	settings := make(map[string]json.RawMessage)
	if err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return "", nil, nil, fmt.Errorf("parse Obsidian graph settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", nil, nil, fmt.Errorf("read Obsidian graph settings: %w", err)
	}
	return settingsPath, raw, settings, nil
}

func saveGraphSettings(settingsPath string, raw []byte, settings map[string]json.RawMessage) error {
	encodedSettings, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Obsidian graph settings: %w", err)
	}
	encodedSettings = append(encodedSettings, '\n')
	if string(raw) == string(encodedSettings) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create Obsidian settings directory: %w", err)
	}
	if err := os.WriteFile(settingsPath, encodedSettings, 0o644); err != nil {
		return fmt.Errorf("write Obsidian graph settings: %w", err)
	}
	return nil
}

func graphPathQuery(indexPath string) string {
	if strings.ContainsAny(indexPath, " \t") {
		return `path:"` + strings.ReplaceAll(indexPath, `"`, `\"`) + `"`
	}
	return "path:" + indexPath
}

type graphColor struct {
	Alpha float64 `json:"a"`
	RGB   int     `json:"rgb"`
}

func (c graphColor) Valid() bool {
	return c.Alpha > 0 && c.Alpha <= 1 && c.RGB >= 0 && c.RGB <= 0xFFFFFF
}

func validGraphColor(raw json.RawMessage) bool {
	var color graphColor
	return json.Unmarshal(raw, &color) == nil && color.Valid()
}

func graphColorForFolder(folder string) graphColor {
	palette := []int{
		0xE67E22,
		0x3498DB,
		0x9B59B6,
		0x2ECC71,
		0xE74C3C,
		0x1ABC9C,
		0xF1C40F,
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(folder))
	return graphColor{Alpha: 1, RGB: palette[int(hash.Sum32())%len(palette)]}
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
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
