package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tiredbooy/internal/models"
)

// BookMetadataStore is Athena's local, reusable catalog cache.
type BookMetadataStore struct{ db *sql.DB }

func NewBookMetadataStore(db *sql.DB) *BookMetadataStore { return &BookMetadataStore{db: db} }

func (s *BookMetadataStore) Get(titleKey string) (*models.BookMetadata, error) {
	var metadata models.BookMetadata
	var authors, genres string
	err := s.db.QueryRow(`SELECT title, authors_json, genres_json, published_year, isbn, source, verified_at FROM book_metadata WHERE title_key = ?`, titleKey).
		Scan(&metadata.Title, &authors, &genres, &metadata.PublishedYear, &metadata.ISBN, &metadata.Source, &metadata.VerifiedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get book metadata: %w", err)
	}
	if err := json.Unmarshal([]byte(authors), &metadata.Authors); err != nil {
		return nil, fmt.Errorf("decode cached authors: %w", err)
	}
	if err := json.Unmarshal([]byte(genres), &metadata.Genres); err != nil {
		return nil, fmt.Errorf("decode cached genres: %w", err)
	}
	return &metadata, nil
}

func (s *BookMetadataStore) Upsert(titleKey string, metadata models.BookMetadata) error {
	authors, err := json.Marshal(metadata.Authors)
	if err != nil {
		return fmt.Errorf("encode authors: %w", err)
	}
	genres, err := json.Marshal(metadata.Genres)
	if err != nil {
		return fmt.Errorf("encode genres: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO book_metadata (title_key, title, authors_json, genres_json, published_year, isbn, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(title_key) DO UPDATE SET title=excluded.title, authors_json=excluded.authors_json, genres_json=excluded.genres_json, published_year=excluded.published_year, isbn=excluded.isbn, source=excluded.source, verified_at=CURRENT_TIMESTAMP`,
		titleKey, strings.TrimSpace(metadata.Title), string(authors), string(genres), metadata.PublishedYear, metadata.ISBN, metadata.Source)
	if err != nil {
		return fmt.Errorf("save book metadata: %w", err)
	}
	return nil
}
