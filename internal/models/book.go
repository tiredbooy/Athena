package models

import "time"

// BookMetadata contains factual catalog data Athena may attach to a book note.
// Empty fields mean "unknown"; they are never guessed from a title.
type BookMetadata struct {
	Title         string    `json:"title"`
	Authors       []string  `json:"authors,omitempty"`
	Genres        []string  `json:"genres,omitempty"`
	PublishedYear int       `json:"published_year,omitempty"`
	ISBN          string    `json:"isbn,omitempty"`
	Source        string    `json:"source"`
	VerifiedAt    time.Time `json:"verified_at,omitempty"`
}
