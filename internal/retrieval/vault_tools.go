package retrieval

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// NoteView is the transport-safe read representation of a note. Storage
// keeps absolute paths internally, but the model only needs a vault-relative
// path and the note's content/metadata to answer the user.
type NoteView struct {
	ID        int64           `json:"id"`
	Title     string          `json:"title"`
	Path      string          `json:"path"`
	Content   string          `json:"content"`
	Type      models.NoteType `json:"type"`
	Done      bool            `json:"done,omitempty"`
	Archived  bool            `json:"archived,omitempty"`
	Trashed   bool            `json:"trashed,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// NoteByRelativePath resolves a vault-relative Markdown path through the index;
// it never accepts an absolute or parent-traversing filesystem path.
func (s *Service) NoteByRelativePath(rel string) (*NoteView, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
		return nil, fmt.Errorf("path must be a vault-relative note path")
	}
	if !strings.HasSuffix(strings.ToLower(rel), ".md") {
		rel += ".md"
	}
	catalog, err := s.Inventory()
	if err != nil {
		return nil, err
	}
	for _, entry := range catalog {
		if entry.Rel == rel {
			note, err := s.noteStore.GetByID(entry.ID)
			if err != nil || note == nil {
				return nil, err
			}
			return s.noteView(note), nil
		}
	}
	return nil, nil
}

func (s *Service) NotesByID(ids []int64) ([]*NoteView, error) {
	if len(ids) == 0 || len(ids) > 8 {
		return nil, fmt.Errorf("note_ids must contain 1 to 8 IDs")
	}
	out := make([]*NoteView, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		note, err := s.noteStore.GetByID(id)
		if err != nil {
			return nil, err
		}
		if note != nil {
			out = append(out, s.noteView(note))
		}
	}
	return out, nil
}

func (s *Service) noteView(note *models.Note) *NoteView {
	return &NoteView{
		ID:        note.ID,
		Title:     note.Title,
		Path:      utils.RelVault(s.vaultPath, note.Path),
		Content:   note.Content,
		Type:      note.Type,
		Done:      note.Done,
		Archived:  note.Archived,
		Trashed:   note.TrashedFrom != "",
		CreatedAt: note.CreatedAt,
		UpdatedAt: note.UpdatedAt,
	}
}

func (s *Service) Tags() (map[string]int, error) {
	counts := map[string]int{}
	catalog, err := s.Inventory()
	if err != nil {
		return nil, err
	}
	for _, entry := range catalog {
		raw, err := utils.ReadNoteFile(filepath.Join(s.vaultPath, filepath.FromSlash(entry.Rel)))
		if err != nil {
			return nil, err
		}
		fm, _, err := parser.ParseMarkdown(raw)
		if err != nil {
			return nil, err
		}
		for _, tag := range fm.Tags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				counts[tag]++
			}
		}
	}
	return counts, nil
}

// Links returns direct wiki-link relationships using note titles as targets.
// It deliberately reports only indexed notes so the model never receives a
// path outside the user's vault.
func (s *Service) Links(noteID int64) (map[string][]CatalogEntry, error) {
	note, err := s.noteStore.GetByID(noteID)
	if err != nil {
		return nil, err
	}
	if note == nil {
		return nil, fmt.Errorf("note %d not found", noteID)
	}
	catalog, err := s.Inventory()
	if err != nil {
		return nil, err
	}
	targets := wikiTargets(note.Content)
	outgoing, incoming := []CatalogEntry{}, []CatalogEntry{}
	for _, entry := range catalog {
		if targets[strings.ToLower(entry.Title)] {
			outgoing = append(outgoing, entry)
		}
		if entry.ID != noteID {
			other, err := s.noteStore.GetByID(entry.ID)
			if err != nil {
				return nil, err
			}
			if other != nil && wikiTargets(other.Content)[strings.ToLower(note.Title)] {
				incoming = append(incoming, entry)
			}
		}
	}
	return map[string][]CatalogEntry{"outgoing": outgoing, "backlinks": incoming}, nil
}

func (s *Service) DuplicateTitles() (map[string][]CatalogEntry, error) {
	catalog, err := s.Inventory()
	if err != nil {
		return nil, err
	}
	groups := map[string][]CatalogEntry{}
	for _, entry := range catalog {
		key := normalize(entry.Title)
		if key != "" {
			groups[key] = append(groups[key], entry)
		}
	}
	for key, entries := range groups {
		if len(entries) < 2 {
			delete(groups, key)
		}
	}
	return groups, nil
}

func wikiTargets(body string) map[string]bool {
	targets := map[string]bool{}
	for _, part := range strings.Split(body, "[[") {
		if !strings.Contains(part, "]]") {
			continue
		}
		target := strings.SplitN(part, "]]", 2)[0]
		target = strings.SplitN(target, "|", 2)[0]
		target = strings.TrimSuffix(target, ".md")
		target = filepath.Base(target)
		if target != "" {
			targets[strings.ToLower(target)] = true
		}
	}
	return targets
}
func normalize(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, strings.ToLower(value))
}
func SortedTags(tags map[string]int) []string {
	out := make([]string, 0, len(tags))
	for tag, count := range tags {
		out = append(out, fmt.Sprintf("%s (%d)", tag, count))
	}
	sort.Strings(out)
	return out
}
