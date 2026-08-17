package notes

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/books"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/utils"
)

// VerifiedWriteActions lists the action types VerifyWrite knows how to check.
// It lives here rather than in the composition root so adding an invariant and
// declaring the action it guards is one edit in one package.
func VerifiedWriteActions() []string {
	return []string{
		"create_note", "create_task", "create_book", "update_book_metadata", "finish_book", "move_note", "update_note", "mark_done",
		"append_note", "replace_section",
		"rename_note", "duplicate_note", "trash_note", "restore_note", "archive_note", "unarchive_note",
		"create_folder", "ensure_folders", "delete_folder", "rename_folder", "move_folder", "link_folders", "unlink_folders", "set_folder_colors", "set_graph_node_size",
		"create_graph_folder",
	}
}

// VerifyWrite re-reads the vault after a successful write and confirms the
// write actually happened. It must not change anything itself; its only job is
// to stop an unverified success being reported to the user.
func (s *Service) VerifyWrite(_ context.Context, action ai.Action) error {
	// Index notes are a derived Obsidian view of the vault. Refresh them after
	// every structural write, never by asking the model to construct wikilinks.
	if err := s.SyncFolderGraph(); err != nil {
		return fmt.Errorf("refresh Obsidian folder graph: %w", err)
	}

	switch action.Type {
	case "create_folder":
		exists, err := s.FolderExists(action.Folder)
		if err != nil {
			return fmt.Errorf("verify create_folder: %w", err)
		}
		if !exists {
			return fmt.Errorf("verify create_folder: folder not found")
		}
		return nil
	case "ensure_folders":
		for _, folder := range action.Paths {
			exists, err := s.FolderExists(folder)
			if err != nil {
				return fmt.Errorf("verify ensure_folders: %w", err)
			}
			if !exists {
				return fmt.Errorf("verify ensure_folders: %q not found", folder)
			}
		}
		return nil
	case "link_folders":
		return s.verifyFolderLinks(action.Folders, true)
	case "unlink_folders":
		return s.verifyFolderLinks(action.Folders, false)
	case "delete_folder":
		exists, err := s.FolderExists(action.Folder)
		if err != nil {
			return fmt.Errorf("verify delete_folder: %w", err)
		}
		if exists {
			return fmt.Errorf("verify delete_folder: folder remains")
		}
		return nil
	case "rename_folder", "move_folder":
		expected, err := expectedFolderDestination(action)
		if err != nil {
			return fmt.Errorf("verify %s: %w", action.Type, err)
		}
		oldExists, err := s.FolderExists(action.Folder)
		if err != nil {
			return fmt.Errorf("verify %s source: %w", action.Type, err)
		}
		newExists, err := s.FolderExists(expected)
		if err != nil {
			return fmt.Errorf("verify %s destination: %w", action.Type, err)
		}
		if oldExists || !newExists {
			return fmt.Errorf("verify %s: source_exists=%t destination=%q exists=%t", action.Type, oldExists, expected, newExists)
		}
		return nil
	case "create_graph_folder":
		if err := s.VerifyFolderInGraph(action.Folder); err != nil {
			return fmt.Errorf("verify create_graph_folder: %w", err)
		}
		return nil
	case "set_folder_colors":
		if err := s.VerifyFolderGraphColors(action.Folder, action.IncludeChildren); err != nil {
			return fmt.Errorf("verify set_folder_colors: %w", err)
		}
		return nil
	case "set_graph_node_size":
		if err := s.VerifyGraphNodeSizeMultiplier(action.NodeSizeMultiplier); err != nil {
			return fmt.Errorf("verify set_graph_node_size: %w", err)
		}
		return nil
	}

	if action.Type == "create_note" || action.Type == "create_task" || action.Type == "create_book" {
		path, err := utils.NotePath(s.vaultPath, action.Folder, action.Title)
		if err != nil {
			return fmt.Errorf("build expected note path: %w", err)
		}
		note, err := s.GetNoteByPath(path)
		if err != nil {
			return fmt.Errorf("verify created note: %w", err)
		}
		if note == nil && action.Type == "create_book" {
			note, err = s.findCanonicalBook(action)
			if err != nil {
				return fmt.Errorf("verify created book: %w", err)
			}
		}
		if note == nil {
			return fmt.Errorf("verify created note: record not found")
		}
		if action.Type == "create_task" && note.Type != models.NoteTypeTask {
			return fmt.Errorf("verify created task: note type is %q", note.Type)
		}
		if action.Type == "create_book" && note.Type != models.NoteTypeBook {
			return fmt.Errorf("verify created book: note type is %q", note.Type)
		}
		return nil
	}

	note, err := s.GetNote(action.NoteID)
	if err != nil {
		return fmt.Errorf("verify %s: %w", action.Type, err)
	}
	if note == nil {
		return fmt.Errorf("verify %s: note %d not found", action.Type, action.NoteID)
	}

	switch action.Type {
	case "update_book_metadata":
		metadata, err := s.BookMetadata(action.NoteID)
		if err != nil {
			return fmt.Errorf("verify update_book_metadata: %w", err)
		}
		if len(action.Authors) > 0 && !sameMetadataValues(metadata.Authors, action.Authors) {
			return fmt.Errorf("verify update_book_metadata: saved authors differ")
		}
		if len(action.Genres) > 0 && !sameMetadataValues(metadata.Genres, action.Genres) {
			return fmt.Errorf("verify update_book_metadata: saved genres differ")
		}
	case "finish_book":
		finished, err := s.IsBookFinished(action.NoteID)
		if err != nil {
			return fmt.Errorf("verify finish_book: %w", err)
		}
		if !finished {
			return fmt.Errorf("verify finish_book: completion timestamp is missing")
		}
	case "move_note":
		expected, err := utils.NotePath(s.vaultPath, action.Folder, note.Title)
		if err != nil {
			return fmt.Errorf("build expected note path: %w", err)
		}
		if note.Path != expected {
			return fmt.Errorf("verify move_note: expected %s, got %s", expected, note.Path)
		}
	case "update_note":
		if note.Content != action.Content {
			return fmt.Errorf("verify update_note: saved content differs")
		}
	case "append_note":
		if !strings.HasSuffix(strings.TrimSpace(note.Content), strings.TrimSpace(action.Content)) {
			return fmt.Errorf("verify append_note: appended content is missing")
		}
	case "replace_section":
		if !strings.Contains(note.Content, strings.TrimSpace(action.Content)) {
			return fmt.Errorf("verify replace_section: replacement content is missing")
		}
	case "mark_done":
		if note.Done != action.Done {
			return fmt.Errorf("verify mark_done: expected done=%t", action.Done)
		}
	case "rename_note":
		if note.Title != action.Title {
			return fmt.Errorf("verify rename_note: expected title %q", action.Title)
		}
	case "duplicate_note":
		title := strings.TrimSpace(action.Title)
		if title == "" {
			title = note.Title + " (copy)"
		}
		folder := strings.TrimSpace(action.Folder)
		if folder == "" {
			folder = utils.RelVault(s.vaultPath, filepath.Dir(note.Path))
			if folder == "." {
				folder = ""
			}
		}
		expected, err := utils.NotePath(s.vaultPath, folder, title)
		if err != nil {
			return fmt.Errorf("verify duplicate_note path: %w", err)
		}
		duplicate, err := s.GetNoteByPath(expected)
		if err != nil {
			return fmt.Errorf("verify duplicate_note: %w", err)
		}
		if duplicate == nil || duplicate.ID == note.ID || duplicate.Content != note.Content || duplicate.Type != note.Type {
			return fmt.Errorf("verify duplicate_note: independent matching copy not found")
		}
	case "trash_note":
		if note.TrashedFrom == "" {
			return fmt.Errorf("verify trash_note: note is not marked as trashed")
		}
	case "restore_note":
		if note.TrashedFrom != "" {
			return fmt.Errorf("verify restore_note: note remains marked as trashed")
		}
	case "archive_note":
		if !note.Archived {
			return fmt.Errorf("verify archive_note: note is not marked as archived")
		}
	case "unarchive_note":
		if note.Archived {
			return fmt.Errorf("verify unarchive_note: note remains archived")
		}
	}
	return nil
}

func (s *Service) findCanonicalBook(action ai.Action) (*models.Note, error) {
	all, err := s.ListNotes()
	if err != nil {
		return nil, err
	}
	wantedFolder, err := utils.CleanFolder(action.Folder)
	if err != nil {
		return nil, err
	}
	for _, note := range all {
		folder := utils.RelVault(s.vaultPath, filepath.Dir(note.Path))
		if folder == "." {
			folder = ""
		}
		if note.Type == models.NoteTypeBook && folder == wantedFolder && books.NormalizeTitle(note.Title) == books.NormalizeTitle(action.Title) {
			return note, nil
		}
	}
	return nil, nil
}

func expectedFolderDestination(action ai.Action) (string, error) {
	oldFolder, err := utils.CleanFolder(action.Folder)
	if err != nil {
		return "", err
	}
	name := oldFolder
	parent := ""
	if index := strings.LastIndex(oldFolder, "/"); index >= 0 {
		parent = oldFolder[:index]
		name = oldFolder[index+1:]
	}
	if action.Type == "rename_folder" {
		name = strings.Trim(strings.TrimSpace(action.NewFolder), "/")
		if name == "" || strings.Contains(name, "/") {
			return "", fmt.Errorf("invalid new folder name %q", action.NewFolder)
		}
	} else {
		parent, err = utils.CleanFolder(action.NewFolder)
		if err != nil {
			return "", err
		}
	}
	if parent == "" {
		return name, nil
	}
	return parent + "/" + name, nil
}

func (s *Service) verifyFolderLinks(folders []string, wantLinked bool) error {
	linksByFolder, err := s.FolderLinks()
	if err != nil {
		return fmt.Errorf("read folder graph: %w", err)
	}
	for _, folder := range folders {
		linkedFolders, ok := linksByFolder[folder]
		if !ok {
			return fmt.Errorf("verify folder graph: %q not found", folder)
		}
		linkedSet := make(map[string]bool, len(linkedFolders))
		for _, linked := range linkedFolders {
			linkedSet[linked] = true
		}
		for _, other := range folders {
			if folder == other {
				continue
			}
			linked := linkedSet[other]
			if linked != wantLinked {
				state := "linked"
				if !wantLinked {
					state = "unlinked"
				}
				return fmt.Errorf("verify folder graph: %q was not %s with %q", folder, state, other)
			}
		}
	}
	return nil
}
