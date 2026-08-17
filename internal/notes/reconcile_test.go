package notes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/utils"
)

// V-07: the vault is edited in Obsidian too. A file written there had no row,
// so it was invisible to listings, search and the model; a row whose file was
// deleted or edited there kept answering with content that no longer exists.
func TestReconcileVaultIndexesUntrackedFilesAndFlagsTheRest(t *testing.T) {
	service, vault := newTestService(t)
	// appdirs prefers XDG over HOME, so blanking HOME alone can still resolve a
	// real config path from the developer's machine.
	t.Setenv("XDG_CONFIG_HOME", "")

	untrackedPath := filepath.Join(vault, "obsidian-note.md")
	untrackedFile := "---\ntitle: Written In Obsidian\nkind: task\n---\n\ncall the bank\n"
	if err := utils.WriteNoteFile(untrackedPath, untrackedFile); err != nil {
		t.Fatalf("write untracked note: %v", err)
	}
	// A generated folder index is Athena's own scaffolding, not a note.
	indexPath := filepath.Join(vault, "work.md")
	if err := utils.WriteNoteFile(indexPath, "---\ntitle: Work\nathena_index: true\n---\n\n# Work\n"); err != nil {
		t.Fatalf("write folder index: %v", err)
	}

	edited, created, err := service.CreateNote(t.Context(), "Edited Outside", "indexed body", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	editedFile := "---\ntitle: Edited Outside\nkind: note\n---\n\nrewritten in obsidian\n"
	if err := os.WriteFile(edited.Path, []byte(editedFile), 0o644); err != nil {
		t.Fatalf("simulate an Obsidian edit: %v", err)
	}

	deleted, created, err := service.CreateNote(t.Context(), "Deleted Outside", "gone", "", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	if err := os.Remove(deleted.Path); err != nil {
		t.Fatalf("simulate a deletion outside Athena: %v", err)
	}

	scan, err := service.ReconcileVault(t.Context())
	if err != nil {
		t.Fatalf("reconcile vault: %v", err)
	}

	if len(scan.Added) != 1 || scan.Added[0] != "obsidian-note.md" {
		t.Fatalf("added = %v, want only the untracked note (never the folder index)", scan.Added)
	}
	if note, err := service.GetNoteByPath(indexPath); err != nil || note != nil {
		t.Fatalf("folder index got a row (%v, err=%v); generated scaffolding must stay out of the catalog", note, err)
	}

	indexedNote, err := service.GetNoteByPath(untrackedPath)
	if err != nil || indexedNote == nil {
		t.Fatalf("untracked note has no row after the scan: %v", err)
	}
	if indexedNote.Title != "Written In Obsidian" || indexedNote.Type != models.NoteTypeTask {
		t.Fatalf("indexed as %q/%q, want the title and kind its frontmatter declares", indexedNote.Title, indexedNote.Type)
	}
	if indexedNote.Content != "call the bank\n" {
		t.Fatalf("indexed body = %q, want the file's body", indexedNote.Content)
	}
	// No silent overwrite: the user's file is read, never rewritten — not even
	// to add the frontmatter Athena writes for its own notes.
	if raw, err := utils.ReadNoteFile(untrackedPath); err != nil || raw != untrackedFile {
		t.Fatalf("the scan rewrote the user's file to %q (err=%v)", raw, err)
	}

	if len(scan.Conflicting) != 1 || scan.Conflicting[0].NoteID != edited.ID {
		t.Fatalf("conflicting = %+v, want the note edited outside Athena", scan.Conflicting)
	}
	storedEdited, err := service.GetNote(edited.ID)
	if err != nil || storedEdited.Content != "indexed body" {
		t.Fatalf("row content = %q err=%v; a conflict must be reported, not resolved by overwriting the row", storedEdited.Content, err)
	}
	if raw, err := utils.ReadNoteFile(edited.Path); err != nil || raw != editedFile {
		t.Fatalf("the scan overwrote the user's edit with %q (err=%v)", raw, err)
	}

	if len(scan.Missing) != 1 || scan.Missing[0].NoteID != deleted.ID {
		t.Fatalf("missing = %+v, want the note whose file disappeared", scan.Missing)
	}
	// Flagged, not deleted: a file that is not there may be a sync that has not
	// finished, and the row is the only record of the note's id and type.
	if stored, err := service.GetNote(deleted.ID); err != nil || stored == nil {
		t.Fatalf("the row for a missing file was deleted (%v, err=%v)", stored, err)
	}

	// A second pass must be a no-op for what the first one indexed.
	rescan, err := service.ReconcileVault(t.Context())
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(rescan.Added) != 0 {
		t.Fatalf("second pass added %v; the files already have rows", rescan.Added)
	}
}

// V-07 + the follow-up V-04 left open: a moveFolder that fails partway leaves
// the row on the old path while the file sits at the new one, and only the next
// move or rename of that note repaired it. A scan that indexed every file
// without a row would turn that into two rows for one note — the same content
// twice in every search result.
func TestReconcileVaultAdoptsAStaleRowInsteadOfIndexingTheNoteTwice(t *testing.T) {
	service, vault := newTestService(t)
	t.Setenv("XDG_CONFIG_HOME", "")

	for _, folder := range []string{"work", "projects"} {
		if _, err := service.CreateFolder(folder); err != nil {
			t.Fatalf("create folder %s: %v", folder, err)
		}
	}
	note, created, err := service.CreateNote(t.Context(), "Quarterly Plan", "body", "work", nil)
	if err != nil || !created {
		t.Fatalf("create note: created=%t err=%v", created, err)
	}
	movedPath := filepath.Join(vault, "projects", filepath.Base(note.Path))
	if err := os.Rename(note.Path, movedPath); err != nil {
		t.Fatalf("simulate a half-finished folder move: %v", err)
	}

	scan, err := service.ReconcileVault(t.Context())
	if err != nil {
		t.Fatalf("reconcile vault: %v", err)
	}
	if len(scan.Added) != 0 {
		t.Fatalf("added %v; the moved note already had a row", scan.Added)
	}
	if len(scan.Missing) != 0 {
		t.Fatalf("missing = %+v; the file was found, only the row was stale", scan.Missing)
	}
	if len(scan.Repaired) != 1 || scan.Repaired[0] != "projects/quarterly-plan.md" {
		t.Fatalf("repaired = %v, want the note repointed at where it now lives", scan.Repaired)
	}

	all, err := service.ListNotes()
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("%d rows after the scan, want one note that moved", len(all))
	}
	if all[0].Path != movedPath {
		t.Fatalf("row still points at %s, want %s", all[0].Path, movedPath)
	}
}

// V-06 wrote `done:` into the Markdown so a rebuild cannot lose it. Reconcile
// is the path that rebuilds, so it has to read the key back — otherwise every
// completed task silently reopens the first time a database is regenerated.
func TestReconcileAdoptsATickedTaskAsDone(t *testing.T) {
	service, vault := newTestService(t)

	path := filepath.Join(vault, "finished.md")
	file := "---\ntitle: Finished Task\nkind: task\ndone: true\n---\n\nalready handled\n"
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatalf("write untracked task: %v", err)
	}

	if _, err := service.ReconcileVault(t.Context()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	adopted, err := service.noteStore.GetByPath(path)
	if err != nil || adopted == nil {
		t.Fatalf("adopted note = %v, err = %v", adopted, err)
	}
	if adopted.Type != models.NoteTypeTask {
		t.Fatalf("adopted type = %q, want task", adopted.Type)
	}
	if !adopted.Done {
		t.Fatal("a task whose frontmatter says done: true was adopted as open; a rebuild would reopen it")
	}
}
