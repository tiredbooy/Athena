package chat

import (
	"testing"

	"github.com/tiredbooy/internal/ai"
)

func TestAsksUserForVaultInventory(t *testing.T) {
	if !asksUserForVaultInventory("Please provide the full list of all books and folders currently on disk.") {
		t.Fatal("expected redundant inventory request to be detected")
	}
	if asksUserForVaultInventory("Which genre should Animal Farm use?") {
		t.Fatal("single classification question is not an inventory request")
	}
}

func TestHasPendingActionsReflectsSessionState(t *testing.T) {
	session := &Session{}
	if session.HasPendingActions() {
		t.Fatal("empty session reported a pending plan")
	}
	session.pendingActions = []ai.Action{{Type: "create_folder", Folder: "work"}}
	if !session.HasPendingActions() {
		t.Fatal("pending action was not reported")
	}
}
