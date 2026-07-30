package models

import "time"

type NoteType string

const (
	NoteTypeNote NoteType = "note"
	NoteTypeTask NoteType = "task"
)

type Note struct {
	ID        int64
	Title     string
	Path      string // absolute path on disk, inside VaultPath
	Content   string
	Type      NoteType
	Done      bool // only meaningful for tasks
	CreatedAt time.Time
	UpdatedAt time.Time
}