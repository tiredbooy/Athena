package retrieval

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// MinSimilarity drops vector hits below this score so unrelated notes
// aren't stuffed into the prompt as "relevant memories".
const MinSimilarity float32 = 0.35

type ContextResult struct {
	Context string
	Results []RetrievedNote
	// Catalog is the full vault inventory (titles + ids). Always present
	// so the model can answer "what notes do I have?" without relying on
	// semantic search matching that meta-question.
	Catalog []CatalogEntry
}

type CatalogEntry struct {
	ID     int64
	Title  string
	Type   models.NoteType
	Done   bool
	Folder string // vault-relative dir, empty = vault root
	Rel    string // vault-relative file path
}

// FolderEntry is the authoritative folder relationship snapshot supplied to
// the model. Paths are vault-relative and every entry represents a directory
// that already exists on disk. LinkedFolders contains only explicit graph
// links; Parent and Children come from the real filesystem hierarchy.
type FolderEntry struct {
	Path          string   `json:"path"`
	Parent        string   `json:"parent,omitempty"`
	Children      []string `json:"children,omitempty"`
	LinkedFolders []string `json:"linked_folders,omitempty"`
}

type RetrievedNote struct {
	ID         int64
	Title      string
	Path       string
	Similarity float32
	Content    string
}

// ProgressFunc receives short descriptions of retrieval work. Keeping this
// callback in the retrieval layer means callers can report progress without
// coupling retrieval to a specific terminal or graphical UI.
type ProgressFunc func(message string)

// Inventory returns the complete note catalog without doing vector search.
// It is used for exact listing requests, where embedding a query would only
// add latency and cannot improve the answer.
func (s *Service) Inventory() ([]CatalogEntry, error) {
	return s.buildCatalog()
}

func (s *Service) BuildContext(
	ctx context.Context,
	query string,
	topK int,
) (*ContextResult, error) {
	return s.BuildContextWithProgress(ctx, query, topK, nil)
}

// BuildContextWithProgress builds the prompt context and reports the actual
// vault steps it takes. A nil progress callback disables reporting.
func (s *Service) BuildContextWithProgress(
	ctx context.Context,
	query string,
	topK int,
	progress ProgressFunc,
) (*ContextResult, error) {

	out := &ContextResult{}

	reportProgress(progress, "Reading vault inventory")
	catalog, err := s.buildCatalog()
	if err != nil {
		return nil, err
	}
	out.Catalog = catalog
	reportProgress(progress, fmt.Sprintf("Inventory ready: %d notes", len(catalog)))
	pathsByID := make(map[int64]string, len(catalog))
	for _, entry := range catalog {
		pathsByID[entry.ID] = entry.Rel
	}
	reportProgress(progress, "Reading folder tree")
	folders, err := s.FolderInventory()
	if err != nil {
		return nil, fmt.Errorf("list folder tree: %w", err)
	}

	var builder strings.Builder
	if len(catalog) == 0 || includeFullCatalog(query) {
		builder.WriteString(formatCatalog(catalog))
	} else {
		builder.WriteString(formatCatalogSummary(catalog))
	}
	builder.WriteString(formatFolders(folders))
	builder.WriteString("\n")

	// Empty vault: skip the embedding round-trip entirely.
	if len(catalog) == 0 {
		out.Context = strings.TrimSpace(builder.String())
		return out, nil
	}

	reportProgress(progress, fmt.Sprintf("Embedding your question with %s", s.ai.EmbedModel()))
	results, err := s.SearchNotes(ctx, query, topK)
	if err != nil {
		return nil, err
	}

	var relevant []string
	for _, r := range results {
		if rel := pathsByID[r.ID]; rel != "" {
			reportProgress(progress, fmt.Sprintf("Reading %s", rel))
		}
		out.Results = append(out.Results, r)

		relevant = append(relevant, fmt.Sprintf(
			"--- note_id=%d | %s (similarity %.2f) ---\n%s",
			r.ID,
			r.Title,
			r.Similarity,
			r.Content,
		))
	}

	if len(relevant) == 0 {
		builder.WriteString("No related notes found for this query.\n")
	} else {
		builder.WriteString("Relevant notes from the user's vault:\n\n")
		builder.WriteString(strings.Join(relevant, "\n\n"))
		builder.WriteString("\n")
	}

	out.Context = strings.TrimSpace(builder.String())
	return out, nil
}

func reportProgress(progress ProgressFunc, message string) {
	if progress != nil {
		progress(message)
	}
}

func (s *Service) buildCatalog() ([]CatalogEntry, error) {
	notes, err := s.noteStore.All()
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	out := make([]CatalogEntry, 0, len(notes))
	for _, n := range notes {
		rel, folder := s.withRel(s.vaultPath, n.Path)
		out = append(out, CatalogEntry{
			ID:     n.ID,
			Title:  n.Title,
			Type:   n.Type,
			Done:   n.Done,
			Folder: folder,
			Rel:    rel,
		})
	}
	return out, nil
}

// FolderInventory returns the real folder tree plus the explicit links stored
// in Athena-managed folder index notes. It intentionally does not infer or
// create destinations: callers can use it to validate an action before any
// filesystem mutation occurs.
func (s *Service) FolderInventory() ([]FolderEntry, error) {
	folders, err := utils.ListFolders(s.vaultPath)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(folders))
	children := make(map[string][]string, len(folders))
	for _, folder := range folders {
		known[folder] = true
		parent := path.Dir(folder)
		if parent == "." {
			parent = ""
		}
		children[parent] = append(children[parent], folder)
	}
	for parent := range children {
		sort.Strings(children[parent])
	}

	out := make([]FolderEntry, 0, len(folders))
	for _, folder := range folders {
		parent := path.Dir(folder)
		if parent == "." {
			parent = ""
		}
		entry := FolderEntry{
			Path:     folder,
			Parent:   parent,
			Children: append([]string(nil), children[folder]...),
		}
		indexPath := filepath.Join(s.vaultPath, filepath.FromSlash(folder)+".md")
		raw, readErr := utils.ReadNoteFile(indexPath)
		if readErr == nil {
			fm, _, parseErr := parser.ParseMarkdown(raw)
			// A user-owned or malformed <folder>.md must not make the real
			// directory disappear from the authoritative tree. It simply has no
			// readable Athena-managed link metadata for this snapshot.
			if parseErr == nil && fm.AthenaIndex {
				for _, linked := range fm.LinkedFolders {
					if linked != folder && known[linked] {
						entry.LinkedFolders = append(entry.LinkedFolders, linked)
					}
				}
				sort.Strings(entry.LinkedFolders)
			}
		} else if !os.IsNotExist(readErr) {
			return nil, fmt.Errorf("read folder index %s: %w", folder, readErr)
		}
		out = append(out, entry)
	}
	return out, nil
}

// SetVaultPath lets the service render tidy relative paths in the catalog.
// Optional — without it, absolute paths are shown.
func (s *Service) withRel(vault, abs string) (rel, folder string) {
	rel = utils.RelVault(vault, abs)
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." || dir == "" {
		return rel, ""
	}
	return rel, dir
}

func formatCatalog(catalog []CatalogEntry) string {
	if len(catalog) == 0 {
		return "Vault inventory: empty (0 notes). The user has not created any notes yet.\nIMPORTANT: If the user is only asking what notes they have, answer that the vault is empty. Do NOT create a note."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Vault inventory (%d notes). Use note_id when updating or moving.\n", len(catalog)))
	b.WriteString("IMPORTANT: If the user is only listing or asking what notes they have, answer from this inventory. Do NOT create or modify notes.\n")
	for _, e := range catalog {
		kind := string(e.Type)
		if kind == "" {
			kind = "note"
		}
		extra := ""
		if e.Type == models.NoteTypeTask {
			if e.Done {
				extra = ", done"
			} else {
				extra = ", open"
			}
		}
		loc := "vault root"
		if e.Folder != "" {
			loc = e.Folder
		} else if e.Rel != "" && !strings.HasPrefix(e.Rel, "/") {
			if d := filepath.ToSlash(filepath.Dir(e.Rel)); d != "." {
				loc = d
			}
		}
		b.WriteString(fmt.Sprintf("- note_id=%d | %s | folder=%s (%s%s)\n", e.ID, e.Title, loc, kind, extra))
	}
	return b.String()
}

func formatCatalogSummary(catalog []CatalogEntry) string {
	return fmt.Sprintf("Vault inventory summary: %d active notes. Exact note IDs and paths are available through find_notes_by_title, list_notes, and get_note. Do not invent an ID; use a read-only tool before changing a note.\n", len(catalog))
}

// includeFullCatalog keeps small local models focused. The complete inventory
// is valuable for exact organization work, but it is distracting for ordinary
// questions whose answer comes from a few semantically relevant notes.
func includeFullCatalog(query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	if containsAnyText(query, []string{
		"what notes", "list notes", "show notes", "which notes", "note_id", "note id", "organize", "move ", "rename ", "delete ", "remove ", "archive ", "restore ", "duplicate ", "update ", "edit ", "append ", "mark ", "finish ", "folder", "directory", "file path", "connect", "disconnect", "link ", "unlink ",
	}) {
		return true
	}
	return containsAnyText(query, []string{"create", "add", "make", "write", "save"}) && containsAnyText(query, []string{"note", "task", "book", "journal", "idea"})
}

func containsAnyText(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func formatFolders(folders []FolderEntry) string {
	if len(folders) == 0 {
		return "Folder tree: vault root only.\n"
	}
	var b strings.Builder
	b.WriteString("Folder tree (authoritative; every listed path already exists):\n")
	for _, folder := range folders {
		b.WriteString("- ")
		b.WriteString(folder.Path)
		if folder.Parent == "" {
			b.WriteString(" | parent=vault root")
		} else {
			b.WriteString(" | parent=")
			b.WriteString(folder.Parent)
		}
		if len(folder.Children) > 0 {
			b.WriteString(" | children=")
			b.WriteString(strings.Join(folder.Children, ", "))
		}
		if len(folder.LinkedFolders) > 0 {
			b.WriteString(" | linked=")
			b.WriteString(strings.Join(folder.LinkedFolders, ", "))
		}
		b.WriteByte('\n')
	}
	return b.String()
}
