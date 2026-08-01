package models

import "time"

// ActionAudit records one attempted model-requested vault operation. ActionJSON
// is stored locally so an unexpected result can be inspected without guessing
// which arguments the model sent to the dispatcher.
type ActionAudit struct {
	ActionType string
	ActionJSON string
	Outcome    string
	Message    string
	Error      string
	CreatedAt  time.Time
}

type NoteType string

const (
	NoteTypeNote NoteType = "note"
	NoteTypeTask NoteType = "task"
)

type Note struct {
	ID      int64
	Title   string
	Path    string // absolute path on disk, inside VaultPath
	Content string
	Type    NoteType
	Done    bool // only meaningful for tasks

	// Archived, if true, means the note has been moved into archive/ and
	// ArchivedFrom holds its original vault-relative path so it can be
	// restored. Empty ArchivedFrom means "not archived".
	Archived     bool
	ArchivedFrom string

	// TrashedFrom holds the note's original vault-relative path while it
	// sits in .trash/. Empty means "not trashed". We keep the row and its
	// chunks/embeddings intact on trash so RestoreNote is a pure move.
	TrashedFrom string

	CreatedAt time.Time
	UpdatedAt time.Time
}
