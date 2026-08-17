package notes

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
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
	_, err := s.syncFolderColors(topLevel, "")
	return err
}

// FolderGraphStyle is the durable visual state of one folder orb, reported back
// so the user can be told what actually changed instead of "done".
type FolderGraphStyle struct {
	Folder string `json:"folder"`
	Color  string `json:"color"`
}

// AddFolderGraphColors styles one real folder and, optionally, only its direct
// children in Obsidian's graph, and reports the resulting color per folder.
//
// An empty color means Athena picks one that contrasts with that folder's
// siblings. In that case an existing color group is never overwritten,
// including one the user created. A non-empty color is an explicit request and
// does replace the current value.
func (s *Service) AddFolderGraphColors(folder string, includeChildren bool, color string) ([]FolderGraphStyle, error) {
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
	return s.syncFolderColors(colored, color)
}

// AddFolderToGraph is what "add X to the graph" actually means. A bare
// directory is not a node: Obsidian's graph is built from Markdown files, which
// is why SyncFolderGraph generates one index note per folder. Creating only the
// directory therefore shows the user no change at all, so this composes the
// three durable effects — directory, index note, orb color — behind one action.
//
// A missing parent is refused by name instead of being created along the way.
// A weak model guessing "work/projects" when there is no "work" would otherwise
// silently grow a second, near-duplicate tree in the vault.
//
// An empty color keeps the G-04 invariant through AddFolderGraphColors: a valid
// existing color is never replaced, and a new orb gets the sibling-contrast
// default.
func (s *Service) AddFolderToGraph(folder, color string) (FolderGraphStyle, error) {
	clean, err := utils.CleanFolder(folder)
	if err != nil {
		return FolderGraphStyle{}, err
	}
	if clean == "" || isManagedFolder(clean) {
		return FolderGraphStyle{}, fmt.Errorf("invalid folder %q", folder)
	}
	if parent := path.Dir(clean); parent != "." {
		parentExists, err := utils.FolderExists(s.vaultPath, parent)
		if err != nil {
			return FolderGraphStyle{}, fmt.Errorf("check parent folder: %w", err)
		}
		if !parentExists {
			return FolderGraphStyle{}, fmt.Errorf("cannot add %q to the graph: parent folder %q does not exist; create it explicitly first", clean, parent)
		}
	}

	existed, err := utils.FolderExists(s.vaultPath, clean)
	if err != nil {
		return FolderGraphStyle{}, fmt.Errorf("check folder: %w", err)
	}
	if !existed {
		if err := utils.EnsureDir(s.vaultPath, clean); err != nil {
			return FolderGraphStyle{}, fmt.Errorf("create folder %q: %w", clean, err)
		}
	}

	styles, err := s.AddFolderGraphColors(clean, false, color)
	if err != nil {
		// The directory, the index note and graph.json are three separate
		// writes, so a later failure would otherwise leave a directory Athena
		// just made sitting invisible in the graph. Undo only what this call
		// created; a folder that was already there is the user's.
		// ponytail: an index note written before the failure is left to the next
		// SyncFolderGraph, whose removeStaleFolderIndexes already deletes
		// Athena-managed indexes with no folder — and which must not touch a
		// user note that happens to sit at that path.
		if !existed {
			os.Remove(filepath.Join(s.vaultPath, filepath.FromSlash(clean)))
		}
		return FolderGraphStyle{}, err
	}
	if len(styles) != 1 {
		return FolderGraphStyle{}, fmt.Errorf("expected one graph color for %q, got %d", clean, len(styles))
	}
	return styles[0], nil
}

// VerifyFolderInGraph re-reads all three things "added to the graph" means, so
// a success report cannot claim more than the vault actually holds.
func (s *Service) VerifyFolderInGraph(folder string) error {
	clean, err := utils.CleanFolder(folder)
	if err != nil {
		return err
	}
	if clean == "" || isManagedFolder(clean) {
		return fmt.Errorf("invalid folder %q", folder)
	}
	exists, err := utils.FolderExists(s.vaultPath, clean)
	if err != nil {
		return fmt.Errorf("check folder: %w", err)
	}
	if !exists {
		return fmt.Errorf("folder %q does not exist", clean)
	}
	raw, err := utils.ReadNoteFile(filepath.Join(s.vaultPath, filepath.FromSlash(clean)+".md"))
	if err != nil {
		return fmt.Errorf("read folder index %s: %w", clean, err)
	}
	frontmatter, _, err := parser.ParseMarkdown(raw)
	if err != nil {
		return fmt.Errorf("parse folder index %s: %w", clean, err)
	}
	if !frontmatter.AthenaIndex {
		return fmt.Errorf("folder index %q is not Athena-managed", clean)
	}
	return s.VerifyFolderGraphColors(clean, false)
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

// syncFolderColors assigns graph colors to folders and reports what each one
// ended up with. An empty requested color means "pick a good one": each folder
// takes the palette entry that contrasts most with its siblings.
//
// requested is an explicit user choice and therefore wins over an existing
// group. Without it, a valid existing color is never replaced (G-04): Athena
// only fills gaps and repairs malformed entries it wrote itself.
func (s *Service) syncFolderColors(folders []string, requested string) ([]FolderGraphStyle, error) {
	if len(folders) == 0 {
		return nil, nil
	}
	var explicit graphColor
	if strings.TrimSpace(requested) != "" {
		parsed, err := ParseGraphColor(requested)
		if err != nil {
			return nil, err
		}
		explicit = parsed
	}

	settingsPath, raw, settings, err := s.loadGraphSettings()
	if err != nil {
		return nil, err
	}

	var groups []map[string]json.RawMessage
	if encoded, ok := settings["colorGroups"]; ok && len(encoded) > 0 && string(encoded) != "null" {
		if err := json.Unmarshal(encoded, &groups); err != nil {
			return nil, fmt.Errorf("parse Obsidian graph color groups: %w", err)
		}
	}
	knownGroups := make(map[string]int, len(groups))
	colorByQuery := make(map[string]graphColor, len(groups))
	for index, group := range groups {
		var query string
		if err := json.Unmarshal(group["query"], &query); err != nil {
			continue
		}
		knownGroups[query] = index
		var color graphColor
		if err := json.Unmarshal(group["color"], &color); err == nil && color.Valid() {
			colorByQuery[query] = color
		}
	}

	// Sorted so the contrast search sees siblings in a stable order and the
	// same vault always produces the same assignment.
	targets := append([]string(nil), folders...)
	sort.Strings(targets)

	styles := make([]FolderGraphStyle, 0, len(targets))
	for _, folder := range targets {
		query := graphPathQuery(folder + ".md")
		index, exists := knownGroups[query]
		current, hasValidColor := colorByQuery[query]

		if explicit.Valid() {
			// An explicit request is the user choosing; it replaces whatever
			// is there.
			writeGraphColorGroup(&groups, knownGroups, colorByQuery, query, explicit)
			styles = append(styles, FolderGraphStyle{Folder: folder, Color: explicit.Hex()})
			continue
		}
		if exists && hasValidColor {
			styles = append(styles, FolderGraphStyle{Folder: folder, Color: current.Hex()})
			continue
		}

		color := contrastingGraphColor(s.siblingColors(folder, colorByQuery))
		if exists {
			// Athena previously wrote a CSS color string, which Obsidian
			// rejects. Repair only malformed settings.
			groups[index]["color"] = json.RawMessage(mustJSON(color))
			colorByQuery[query] = color
		} else {
			writeGraphColorGroup(&groups, knownGroups, colorByQuery, query, color)
		}
		styles = append(styles, FolderGraphStyle{Folder: folder, Color: color.Hex()})
	}

	sort.SliceStable(groups, func(i, j int) bool {
		var left, right string
		_ = json.Unmarshal(groups[i]["query"], &left)
		_ = json.Unmarshal(groups[j]["query"], &right)
		return left < right
	})
	encodedGroups, err := json.Marshal(groups)
	if err != nil {
		return nil, fmt.Errorf("encode Obsidian graph color groups: %w", err)
	}
	settings["colorGroups"] = encodedGroups
	if err := saveGraphSettings(settingsPath, raw, settings); err != nil {
		return nil, err
	}
	return styles, nil
}

func writeGraphColorGroup(groups *[]map[string]json.RawMessage, knownGroups map[string]int, colorByQuery map[string]graphColor, query string, color graphColor) {
	if index, exists := knownGroups[query]; exists {
		(*groups)[index]["color"] = json.RawMessage(mustJSON(color))
	} else {
		knownGroups[query] = len(*groups)
		*groups = append(*groups, map[string]json.RawMessage{
			"query": json.RawMessage(mustJSON(query)),
			"color": json.RawMessage(mustJSON(color)),
		})
	}
	colorByQuery[query] = color
}

// siblingColors returns the colors already in use by folders that sit next to
// this one in the graph, which is what a new orb has to stand out against.
func (s *Service) siblingColors(folder string, colorByQuery map[string]graphColor) []graphColor {
	parent := path.Dir(folder)
	all, err := utils.ListFolders(s.vaultPath)
	if err != nil {
		return nil
	}
	used := make([]graphColor, 0, len(all))
	for _, candidate := range visibleFolders(all) {
		if candidate == folder || path.Dir(candidate) != parent {
			continue
		}
		if color, ok := colorByQuery[graphPathQuery(candidate+".md")]; ok {
			used = append(used, color)
		}
	}
	return used
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

// graphPalette is ordered, so ties in the contrast search resolve the same way
// on every machine and every run.
var graphPalette = []int{
	0xE67E22,
	0x3498DB,
	0x9B59B6,
	0x2ECC71,
	0xE74C3C,
	0x1ABC9C,
	0xF1C40F,
}

// contrastingGraphColor picks the palette entry that stands out most from the
// colors already in use next to this folder.
//
// The previous rule hashed the folder name. That was deterministic but blind:
// two adjacent orbs could land on neighbouring palette entries, which is
// exactly what "make this folder stand out" asks Athena to avoid. Choosing the
// entry whose nearest neighbour is furthest away gives a stable answer that
// actually reads as different on screen. Ties keep the earliest palette entry,
// so the same vault always produces the same graph.
func contrastingGraphColor(used []graphColor) graphColor {
	best, bestDistance := graphPalette[0], -1
	for _, candidate := range graphPalette {
		nearest := -1
		for _, existing := range used {
			distance := rgbDistance(candidate, existing.RGB)
			if nearest < 0 || distance < nearest {
				nearest = distance
			}
		}
		if nearest < 0 {
			// Nothing to contrast against: the first palette entry is as good
			// as any, and staying deterministic matters more.
			return graphColor{Alpha: 1, RGB: graphPalette[0]}
		}
		if nearest > bestDistance {
			best, bestDistance = candidate, nearest
		}
	}
	return graphColor{Alpha: 1, RGB: best}
}

// rgbDistance is squared Euclidean distance in RGB. Squared is enough: only the
// ordering matters, and skipping the square root keeps this in integers.
func rgbDistance(left, right int) int {
	dr := (left >> 16 & 0xFF) - (right >> 16 & 0xFF)
	dg := (left >> 8 & 0xFF) - (right >> 8 & 0xFF)
	db := (left & 0xFF) - (right & 0xFF)
	return dr*dr + dg*dg + db*db
}

// ParseGraphColor accepts an explicit "#RRGGBB" (or "RRGGBB") request.
func ParseGraphColor(value string) (graphColor, error) {
	clean := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "#"))
	if len(clean) != 6 {
		return graphColor{}, fmt.Errorf("color %q must be a #RRGGBB hex value", value)
	}
	rgb, err := strconv.ParseInt(clean, 16, 32)
	if err != nil {
		return graphColor{}, fmt.Errorf("color %q must be a #RRGGBB hex value", value)
	}
	return graphColor{Alpha: 1, RGB: int(rgb)}, nil
}

func (c graphColor) Hex() string {
	return fmt.Sprintf("#%06X", c.RGB)
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
