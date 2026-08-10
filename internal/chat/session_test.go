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

func TestPendingPlanIsCopiedAndSingleUse(t *testing.T) {
	session := &Session{pendingPlan: &PendingPlan{
		ID:      "plan-1",
		Actions: []ai.Action{{Type: "create_folder", Folder: "work"}},
	}}

	plan := session.PendingPlan()
	plan.Actions[0].Folder = "changed-outside-session"
	if session.pendingPlan.Actions[0].Folder != "work" {
		t.Fatal("a caller mutated the engine-owned plan")
	}
	if _, err := session.RejectPlan("plan-other"); err == nil {
		t.Fatal("stale plan ID was accepted")
	}
	if _, err := session.RejectPlan("plan-1"); err != nil {
		t.Fatalf("reject current plan: %v", err)
	}
	if session.HasPendingActions() {
		t.Fatal("rejected plan remained pending")
	}
	if plan.Actions[0].Folder != "changed-outside-session" {
		t.Fatal("test setup did not retain its local copy")
	}
}

func TestActivityEventOnlyExposesVaultRelativePaths(t *testing.T) {
	session := &Session{}
	if got := session.activityEvent("Reading work/rumera/plan.md").Path; got != "work/rumera/plan.md" {
		t.Fatalf("relative vault path = %q", got)
	}
	if got := session.activityEvent("Reading vault inventory").Path; got != "" {
		t.Fatalf("non-path activity exposed as path: %q", got)
	}
	if got := session.activityEvent("Reading /home/user/private.md").Path; got != "" {
		t.Fatalf("absolute path exposed: %q", got)
	}
}

func TestActionActivityIsShownAsExecution(t *testing.T) {
	session := &Session{}
	if got := session.activityEvent(`Creating note "Rumera"`); got.Phase != "executing" {
		t.Fatalf("creating activity phase = %q, want executing", got.Phase)
	}
	if got := session.activityEvent(`Created note "Rumera"`); got.Phase != "executing" {
		t.Fatalf("completed activity phase = %q, want executing", got.Phase)
	}
}

func TestImplicitFolderCreationIsBlockedForRelationshipRequests(t *testing.T) {
	actions := []ai.Action{{Type: "ensure_folders", Paths: []string{"invented"}}}
	if warning := implicitFolderCreationWarning("remove the parent and connect this folder to work", actions); warning == "" {
		t.Fatal("expected implicit folder creation warning")
	}
	if warning := implicitFolderCreationWarning("create a folder called work", actions); warning != "" {
		t.Fatalf("explicit folder creation was blocked: %q", warning)
	}
	if warning := implicitFolderCreationWarning("create a note in books/reading/computer science", actions); warning != "" {
		t.Fatalf("named note destination was blocked: %q", warning)
	}
	bookRequest := `go into my books folder and i want you to add new book that i started reading into correct "reading/book genre" i started reading Project Mary Hill and Thinking Fast, And slow`
	if warning := implicitFolderCreationWarning(bookRequest, actions); warning != "" {
		t.Fatalf("named book destinations were blocked: %q", warning)
	}
	if warning := implicitFolderCreationWarning("start a task under work/projects", actions); warning != "" {
		t.Fatalf("named task destination was blocked: %q", warning)
	}
}

func TestHasPendingActionsReflectsSessionState(t *testing.T) {
	session := &Session{}
	if session.HasPendingActions() {
		t.Fatal("empty session reported a pending plan")
	}
	session.pendingPlan = &PendingPlan{ID: "plan-1", Actions: []ai.Action{{Type: "create_folder", Folder: "work"}}}
	if !session.HasPendingActions() {
		t.Fatal("pending action was not reported")
	}
}
