package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/tiredbooy/internal/models"
)

// ActionAuditStore persists dispatcher outcomes. It intentionally has no read
// API yet: recording reliable facts comes before exposing an audit UI.
type ActionAuditStore struct {
	db *sql.DB
}

func NewActionAuditStore(db *sql.DB) *ActionAuditStore {
	return &ActionAuditStore{db: db}
}

func (s *ActionAuditStore) Record(ctx context.Context, entry models.ActionAudit) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO action_audit (action_type, action_json, outcome, message, error)
		 VALUES (?, ?, ?, ?, ?)`,
		entry.ActionType, entry.ActionJSON, entry.Outcome, entry.Message, entry.Error,
	)
	if err != nil {
		return fmt.Errorf("record action audit: %w", err)
	}
	return nil
}
