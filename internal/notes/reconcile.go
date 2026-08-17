package notes

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/utils"
)

// Startup vault reconcile (V-07).
//
// The vault is a real folder the user also opens in Obsidian, so the
// filesystem and SQLite drift apart between runs: a note written there has no
// row and is invisible to search and to the model, and a note moved or deleted
// there leaves a row pointing at a file that is no longer under it.
//
// ReconcileVault closes only the gap that has one safe answer — a file with no
// row gets a row — and reports the rest. Neither side is the single source of
// truth, and both destructive readings are plausible: a file that is not there
// may be a sync that has not finished rather than a deletion, and a file whose
// text differs from its row may be the newer copy or the truncated one. So
// this pass never writes to a user's file, never rewrites a row's content from
// disk, and never deletes a row.

// VaultScan is the result of one reconcile pass. Every path is vault-relative
// so a caller can print it as the user sees it.
type VaultScan struct {
	// Added are files that had no row and were indexed by this pass. They were
	// read, never written: an untracked file keeps whatever frontmatter (or
	// none) the user gave it.
	Added []string

	// Repaired are rows that pointed at a missing file and were repointed at
	// the same note found elsewhere in the vault. Only the row moved.
	Repaired []string

	// Missing are rows whose file could not be located. They are flagged, never
	// deleted — the row is the only thing that still remembers the note's id,
	// type and done state, and deleting it on a sync that is mid-flight would
	// destroy exactly what the user came back for.
	Missing []VaultIssue

	// Conflicting are notes this pass refuses to settle on its own: the text on
	// disk differs from the indexed copy, or the file could not be read or
	// parsed. Nothing was written for any of them.
	Conflicting []VaultIssue
}

// VaultIssue is one note the scan reports instead of repairing.
type VaultIssue struct {
	Path   string // vault-relative
	Title  string // the row's title; empty when the file has no row
	NoteID int64  // 0 when the file has no row
	Reason string
}

// ReconcileVault brings SQLite back in line with the vault folder as far as it
// safely can, and returns what it found. The scan is read-only towards the
// filesystem; see the package comment above for why it reports rather than
// repairs the ambiguous cases.
func (s *Service) ReconcileVault(ctx context.Context) (VaultScan, error) {
	var scan VaultScan

	// Live rows only. Trashed rows point inside .trash, which this scan never
	// walks: anything found there would be re-indexed straight back into RAG,
	// and that rule outranks completeness here.
	tracked, err := s.noteStore.All()
	if err != nil {
		return scan, fmt.Errorf("list notes for reconcile: %w", err)
	}

	// Filled before the walk so a row repaired below claims its file and the
	// walk cannot index the same note a second time.
	indexed := make(map[string]bool, len(tracked))
	for _, note := range tracked {
		body, readErr := s.readNoteBody(note.Path)
		if errors.Is(readErr, fs.ErrNotExist) {
			// This is the case V-04 left open: a moveFolder that fails partway
			// leaves the row on the old path while the file sits at the new
			// one, and until now only the next move or rename of that note
			// repaired it. reconcileMissingNotePath adopts a file only when its
			// name and its frontmatter title both match, and refuses when
			// several match, so a same-named stranger is never claimed.
			if repairErr := s.reconcileMissingNotePath(note); repairErr != nil {
				scan.Missing = append(scan.Missing, s.issueFor(note, repairErr.Error()))
				continue
			}
			scan.Repaired = append(scan.Repaired, utils.RelVault(s.vaultPath, note.Path))
			indexed[note.Path] = true
			continue
		}
		// Claimed even when the file cannot be read, so a note that is merely
		// unreadable today is not indexed again as if it were new.
		indexed[note.Path] = true
		if readErr != nil {
			scan.Conflicting = append(scan.Conflicting, s.issueFor(note, readErr.Error()))
			continue
		}
		// A trailing newline is not an edit: editors add one on save, and a
		// scan that cries wolf on every note teaches the user to ignore it.
		if strings.TrimSpace(body) != strings.TrimSpace(note.Content) {
			scan.Conflicting = append(scan.Conflicting, s.issueFor(note, "edited outside Athena; the indexed copy is stale"))
		}
	}

	walkErr := filepath.WalkDir(s.vaultPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := utils.RelVault(s.vaultPath, path)
		if entry.IsDir() {
			if rel == ".trash" || rel == ".obsidian" {
				return filepath.SkipDir
			}
			return nil
		}
		if indexed[path] || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}

		raw, err := utils.ReadNoteFile(path)
		if err != nil {
			scan.Conflicting = append(scan.Conflicting, VaultIssue{Path: rel, Reason: fmt.Sprintf("untracked file could not be read: %v", err)})
			return nil
		}
		frontmatter, body, err := parser.ParseMarkdown(raw)
		if err != nil {
			scan.Conflicting = append(scan.Conflicting, VaultIssue{Path: rel, Reason: fmt.Sprintf("untracked file's frontmatter does not parse: %v", err)})
			return nil
		}
		// Folder index notes are generated by SyncFolderGraph and rewritten
		// whenever the vault's shape changes. Indexing one would feed Athena's
		// own scaffolding back into search results and model context.
		if frontmatter.AthenaIndex {
			return nil
		}

		note := &models.Note{
			Title:   titleForFile(frontmatter, entry.Name()),
			Path:    path,
			Content: body,
			Type:    noteTypeFor(frontmatter),
			// The file is the durable record here, so a ticked task that is
			// re-adopted — after a database rebuild, or a sync that arrived
			// before the row did — must come back ticked. Dropping this would
			// silently reopen every completed task on a rebuild, which is the
			// data loss V-06 wrote `done:` into the Markdown to prevent.
			Done: frontmatter.Done,
		}
		id, err := s.noteStore.Create(note)
		if err != nil {
			return fmt.Errorf("index %s: %w", rel, err)
		}
		note.ID = id
		scan.Added = append(scan.Added, rel)
		// Same split as createNote: the row is saved and an embedding failure
		// must not lose it. The scan does stop here, because a provider that is
		// unreachable now will be unreachable for every remaining file.
		if err := s.embedNote(ctx, note); err != nil {
			return fmt.Errorf("indexed %s, but embedding failed: %w", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		return scan, fmt.Errorf("scan vault: %w", walkErr)
	}
	return scan, nil
}

func (s *Service) issueFor(note *models.Note, reason string) VaultIssue {
	return VaultIssue{
		Path:   utils.RelVault(s.vaultPath, note.Path),
		Title:  note.Title,
		NoteID: note.ID,
		Reason: reason,
	}
}

// titleForFile falls back to the filename, which is what Obsidian shows as the
// note's name. A prettier invented title would make Athena's listing disagree
// with the editor the file came from.
func titleForFile(frontmatter parser.Frontmatter, filename string) string {
	if title := strings.TrimSpace(frontmatter.Title); title != "" {
		return title
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

// noteTypeFor trusts `kind:` only when it names a type Athena knows; a plugin's
// own key or a typo becomes a plain note rather than a row carrying a type that
// every listing switching on it would mishandle.
//
// The done state comes from the frontmatter's own `done:` key, read alongside
// this, so a task adopted from disk keeps whether it was finished.
func noteTypeFor(frontmatter parser.Frontmatter) models.NoteType {
	switch kind := models.NoteType(strings.TrimSpace(frontmatter.Kind)); kind {
	case models.NoteTypeTask, models.NoteTypeBook:
		return kind
	default:
		return models.NoteTypeNote
	}
}
