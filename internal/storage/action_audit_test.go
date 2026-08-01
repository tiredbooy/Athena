package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tiredbooy/internal/models"
)

func TestActionAuditStoreRecordsOutcome(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := NewActionAuditStore(db)
	err = store.Record(context.Background(), models.ActionAudit{
		ActionType: "create_note",
		ActionJSON: `{"type":"create_note","title":"Plan"}`,
		Outcome:    "succeeded",
		Message:    "Created note \"Plan\"",
	})
	if err != nil {
		t.Fatalf("record action audit: %v", err)
	}

	var actionType, outcome string
	if err := db.QueryRow(`SELECT action_type, outcome FROM action_audit`).Scan(&actionType, &outcome); err != nil {
		t.Fatalf("read action audit: %v", err)
	}
	if actionType != "create_note" || outcome != "succeeded" {
		t.Fatalf("audit row = (%q, %q)", actionType, outcome)
	}
}
