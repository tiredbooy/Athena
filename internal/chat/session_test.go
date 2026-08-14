package chat

import (
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

func TestAsksUserForVaultInventory(t *testing.T) {
	if !asksUserForVaultInventory("Please provide the full list of all books and folders currently on disk.") {
		t.Fatal("expected redundant inventory request to be detected")
	}
	if asksUserForVaultInventory("Which genre should Animal Farm use?") {
		t.Fatal("single classification question is not an inventory request")
	}
}

func TestPermissionQuestionIsTrackedAsClarification(t *testing.T) {
	if !looksLikeClarifyingQuestion("May I create Psychology and Science Fiction, then move the books?") {
		t.Fatal("permission question was treated as a completed answer")
	}
}

func TestMutationPermissionQuestionIsDeferredToPlanReview(t *testing.T) {
	if !asksForActionPermission("May I create Psychology and Science Fiction, then move the books?") {
		t.Fatal("redundant mutation permission was not detected")
	}
	if asksForActionPermission("Should I classify this book as Science Fiction?") {
		t.Fatal("classification question was mistaken for plan approval")
	}
}

func TestCancelDiscardsPendingConversationalTask(t *testing.T) {
	session := &Session{history: []models.Message{{Role: "system", Content: "rules"}}, pendingTask: &PendingTask{OriginalGoal: "organize books", Question: "Which genre?"}}
	reply, err := session.cancelPending()
	if err != nil || reply != "Pending task discarded." || session.pendingTask != nil {
		t.Fatalf("reply=%q pending=%+v err=%v", reply, session.pendingTask, err)
	}
}

func TestAgentRunContractRequiresStructuredMutationPlans(t *testing.T) {
	contract := agentRunContractMessage().Content
	if !containsAny(contract, []string{"MUST call it with a non-empty actions array"}) {
		t.Fatalf("agent contract does not require propose_actions for mutations: %s", contract)
	}
	if !containsAny(contract, []string{"Do not describe intended changes in prose without that tool call"}) {
		t.Fatalf("agent contract still permits prose-only mutation plans: %s", contract)
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

func TestTrackedReadingRequestUsesBookActionsAndDropsDuplicates(t *testing.T) {
	goal := `add books that i started reading into books/reading`
	actions := []ai.Action{
		{Type: "create_note", Title: "Thinking Fast And Slow", Content: "invented metadata", Folder: "books/reading"},
		{Type: "create_note", Title: "Thinking Fast and Slow", Content: "different body", Folder: "books/reading"},
		{Type: "create_note", Title: "Unrelated Journal", Content: "keep me", Folder: "journal"},
	}

	got := normalizeTrackedBookActions(goal, actions)
	if len(got) != 2 {
		t.Fatalf("actions = %+v, want one tracked book and one unrelated note", got)
	}
	if got[0].Type != "create_book" || got[0].Content != "" || got[0].Tags != nil {
		t.Fatalf("book action = %+v", got[0])
	}
	if got[1].Type != "create_note" || got[1].Title != "Unrelated Journal" {
		t.Fatalf("unrelated action changed: %+v", got[1])
	}
}
