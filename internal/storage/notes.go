package storage

import (
	"database/sql"
	"fmt"

	"github.com/tiredbooy/internal/models"
)

type NoteStore struct {
	db *sql.DB
}

func NewNoteStore(db *sql.DB) *NoteStore {
	return &NoteStore{db: db}
}

func (s *NoteStore) Create(n *models.Note) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO notes (title, path, content, note_type, done) VALUES (?, ?, ?, ?, ?)`,
		n.Title, n.Path, n.Content, string(n.Type), n.Done,
	)
	if err != nil {
		return 0, fmt.Errorf("insert note: %w", err)
	}
	return res.LastInsertId()
}

func (s *NoteStore) GetByPath(path string) (*models.Note, error) {
	var n models.Note
	var noteType string

	err := s.db.QueryRow(
		`SELECT id, title, path, content, note_type, done, created_at, updated_at FROM notes WHERE path = ?`,
		path,
	).Scan(&n.ID, &n.Title, &n.Path, &n.Content, &noteType, &n.Done, &n.CreatedAt, &n.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get note by path: %w", err)
	}

	n.Type = models.NoteType(noteType)
	return &n, nil
}

func (s *NoteStore) GetByID(id int64) (*models.Note, error) {
	var n models.Note
	var noteType string

	err := s.db.QueryRow(
		`SELECT id, title, path, content, note_type, done, created_at, updated_at FROM notes WHERE id = ?`,
		id,
	).Scan(&n.ID, &n.Title, &n.Path, &n.Content, &noteType, &n.Done, &n.CreatedAt, &n.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get note by id: %w", err)
	}

	n.Type = models.NoteType(noteType)
	return &n, nil
}

func (s *NoteStore) Update(n *models.Note) error {
	_, err := s.db.Exec(
		`UPDATE notes SET title = ?, content = ?, note_type = ?, done = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		n.Title, n.Content, string(n.Type), n.Done, n.ID,
	)
	if err != nil {
		return fmt.Errorf("update note: %w", err)
	}
	return nil
}

func (s *NoteStore) All() ([]*models.Note, error) {
	rows, err := s.db.Query(
		`SELECT id, title, path, content, note_type, done, created_at, updated_at FROM notes ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query notes: %w", err)
	}
	defer rows.Close()

	var notes []*models.Note
	for rows.Next() {
		var n models.Note
		var noteType string
		if err := rows.Scan(&n.ID, &n.Title, &n.Path, &n.Content, &noteType, &n.Done, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan note: %w", err)
		}
		n.Type = models.NoteType(noteType)
		notes = append(notes, &n)
	}
	return notes, rows.Err()
}
