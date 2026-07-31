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
		`INSERT INTO notes (title, path, content, note_type, done, archived, archived_from, trashed_from)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Title, n.Path, n.Content, string(n.Type), n.Done, n.Archived, n.ArchivedFrom, n.TrashedFrom,
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
		`SELECT id, title, path, content, note_type, done, archived, archived_from, trashed_from, created_at, updated_at
		 FROM notes WHERE path = ?`,
		path,
	).Scan(&n.ID, &n.Title, &n.Path, &n.Content, &noteType, &n.Done, &n.Archived, &n.ArchivedFrom, &n.TrashedFrom, &n.CreatedAt, &n.UpdatedAt)

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
		`SELECT id, title, path, content, note_type, done, archived, archived_from, trashed_from, created_at, updated_at
		 FROM notes WHERE id = ?`,
		id,
	).Scan(&n.ID, &n.Title, &n.Path, &n.Content, &noteType, &n.Done, &n.Archived, &n.ArchivedFrom, &n.TrashedFrom, &n.CreatedAt, &n.UpdatedAt)

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
		`UPDATE notes
		 SET title = ?, path = ?, content = ?, note_type = ?, done = ?,
		     archived = ?, archived_from = ?, trashed_from = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		n.Title, n.Path, n.Content, string(n.Type), n.Done, n.Archived, n.ArchivedFrom, n.TrashedFrom, n.ID,
	)
	if err != nil {
		return fmt.Errorf("update note: %w", err)
	}
	return nil
}

// All returns every non-trashed note. Trashed notes are hidden from normal
// listings on purpose — use Trashed() to see what's in .trash.
func (s *NoteStore) All() ([]*models.Note, error) {
	return s.query(`SELECT id, title, path, content, note_type, done, archived, archived_from, trashed_from, created_at, updated_at
	                 FROM notes WHERE trashed_from = '' ORDER BY updated_at DESC`)
}

// Trashed returns every note currently sitting in .trash.
func (s *NoteStore) Trashed() ([]*models.Note, error) {
	return s.query(`SELECT id, title, path, content, note_type, done, archived, archived_from, trashed_from, created_at, updated_at
	                 FROM notes WHERE trashed_from != '' ORDER BY updated_at DESC`)
}

func (s *NoteStore) query(q string, args ...any) ([]*models.Note, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query notes: %w", err)
	}
	defer rows.Close()

	var notes []*models.Note
	for rows.Next() {
		var n models.Note
		var noteType string
		if err := rows.Scan(&n.ID, &n.Title, &n.Path, &n.Content, &noteType, &n.Done, &n.Archived, &n.ArchivedFrom, &n.TrashedFrom, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan note: %w", err)
		}
		n.Type = models.NoteType(noteType)
		notes = append(notes, &n)
	}
	return notes, rows.Err()
}
