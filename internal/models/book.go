package models

import "time"

// BookMetadata contains factual catalog data Athena may attach to a book note.
// Empty fields mean "unknown"; they are never guessed from a title.
type BookMetadata struct {
	Title         string
	Authors       []string
	Genres        []string
	PublishedYear int
	ISBN          string
	Source        string
	VerifiedAt    time.Time
}
