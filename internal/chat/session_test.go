package chat

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiredbooy/internal/agent"
	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/tools"
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

// F-03: a UI clearing its transcript must not be mistaken for this. Reset is
// the only thing that drops engine-side conversation state.
func TestResetClearsHistoryPendingPlanAndPendingTask(t *testing.T) {
	session := NewSession(NewLoop(nil, nil, nil, nil, nil, nil))
	systemPrompt := session.history[0].Content

	session.append("organize my books", "Which shelf?")
	session.pendingPlan = &PendingPlan{ID: "plan-1"}
	session.pendingTask = &PendingTask{OriginalGoal: "organize my books"}

	session.Reset()

	if len(session.history) != 1 {
		t.Fatalf("history has %d messages, want only the system prompt", len(session.history))
	}
	if session.history[0].Content != systemPrompt {
		t.Fatal("reset replaced the system prompt instead of keeping it")
	}
	if session.pendingPlan != nil {
		t.Fatal("reset left a pending plan the user believes is gone")
	}
	if session.pendingTask != nil {
		t.Fatal("reset left a pending clarification task")
	}
	if session.HasPendingActions() {
		t.Fatal("HasPendingActions still true after reset")
	}
}

// U-13: a plan awaiting review blocks new goals, not diagnostics. The user must
// be able to inspect the engine before deciding whether to confirm it.
func TestInspectionCommandsRunWhileAPlanIsPending(t *testing.T) {
	provider := &slowProvider{started: make(chan struct{}), release: make(chan struct{})}
	session := NewSession(NewLoop(provider, map[string]ai.ChatProvider{"ollama": provider}, nil, nil, nil, nil))
	session.pendingPlan = &PendingPlan{ID: "plan-1", Actions: []ai.Action{{Type: "trash_note", NoteID: 3}}}

	const review = "A change plan is awaiting review. Type /confirm to apply it or /cancel to discard it."
	reply, err := session.Submit(context.Background(), "/compact", nil, nil)
	if err != nil {
		t.Fatalf("/compact while a plan is pending: %v", err)
	}
	if reply == review {
		t.Fatal("/compact was refused while a plan was pending")
	}
	if !session.HasPendingActions() {
		t.Fatal("an inspection command dropped the pending plan")
	}

	// A model swap and any new goal still wait for the review decision.
	for _, blocked := range []string{"/model 2", "organize my books"} {
		reply, err := session.Submit(context.Background(), blocked, nil, nil)
		if err != nil || reply != review {
			t.Fatalf("%q reply=%q err=%v, want the review prompt", blocked, reply, err)
		}
	}

	if !allowedWhilePlanPending("/doctor") || !allowedWhilePlanPending("/models") {
		t.Fatal("read-only diagnostics are not allowlisted")
	}
	if allowedWhilePlanPending("/model 2") || allowedWhilePlanPending("/reset") {
		t.Fatal("a state-changing command was allowlisted")
	}
}

// F-05: /confirm is the user's one approval. If applying it is cancelled or
// fails, the work that was never verified comes back as a new plan instead of
// vanishing — and the part that already succeeded does not come back with it.
func TestInterruptedPlanApplyRestagesOnlyUnverifiedActions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dispatcher := tools.NewDispatcher()
	dispatcher.Register("mark_done", func(_ context.Context, action ai.Action) (string, error) {
		if action.NoteID == 2 {
			// Stands in for the user pressing Escape while the approved plan is
			// being applied: the turn context dies mid-batch.
			cancel()
			return "", errors.New("vault write interrupted")
		}
		return "marked note 1 done", nil
	})

	approved := []ai.Action{{Type: "mark_done", NoteID: 1}, {Type: "mark_done", NoteID: 2}}
	session := NewSession(NewLoop(nil, nil, nil, nil, dispatcher, nil))
	session.pendingPlan = &PendingPlan{
		ID:      "plan-approved",
		Actions: approved,
		run:     agent.NewRunState("run-1", "mark both reading tasks done", nil),
	}

	reply, err := session.approvePlan(ctx, "plan-approved", runObserver{session: session})
	if err == nil {
		t.Fatalf("interrupted apply reported success: %q", reply)
	}
	if !session.HasPendingActions() {
		t.Fatal("the approved plan was lost when applying it was interrupted")
	}
	restaged := session.PendingPlan()
	if restaged.ID == "plan-approved" {
		t.Fatal("the re-staged plan reused the single-use plan ID")
	}
	if len(restaged.Actions) != 1 || restaged.Actions[0].NoteID != 2 {
		t.Fatalf("re-staged actions = %+v, want only the unverified note 2", restaged.Actions)
	}
	if !strings.Contains(err.Error(), restaged.ID) {
		t.Fatalf("error does not name the re-staged plan: %v", err)
	}
	ledger := session.LastLedger()
	if len(ledger) != 2 || ledger[0].Status != "succeeded" || ledger[1].Status != "failed" {
		t.Fatalf("verified record of the interrupted apply = %+v", ledger)
	}
}

// slowProvider blocks in the model call until released, standing in for a
// local model that takes minutes to answer.
type slowProvider struct {
	started  chan struct{}
	release  chan struct{}
	startOne sync.Once
}

func (p *slowProvider) Name() string        { return "Ollama" }
func (p *slowProvider) ChatModel() string   { return "slow-model" }
func (p *slowProvider) SetChatModel(string) {}
func (p *slowProvider) ChatModels(context.Context) ([]ai.ModelInfo, error) {
	return []ai.ModelInfo{{Name: "slow-model"}}, nil
}
func (p *slowProvider) block(ctx context.Context) {
	p.startOne.Do(func() { close(p.started) })
	select {
	case <-p.release:
	case <-ctx.Done():
	}
}
func (p *slowProvider) ChatWithToolsResult(ctx context.Context, _ []models.Message, _ []models.ToolDefinition) (ai.ToolChatResult, error) {
	p.block(ctx)
	return ai.ToolChatResult{Message: models.Message{Role: "assistant", Content: "done"}}, nil
}
func (p *slowProvider) StreamChatWith(ctx context.Context, _ []models.Message, _ ai.StreamCallbacks) (string, error) {
	p.block(ctx)
	return "done", nil
}

// F-04: a turn holds turnMu for minutes. Anything a UI needs while that turn
// runs — the footer, the model picker, plan state — must not wait on it.
func TestSessionStaysInspectableDuringATurn(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	retrievalService := retrieval.NewService(t.TempDir(), storage.NewNoteStore(db), storage.NewChunkStore(db), taskStateEmbedder{})

	provider := &slowProvider{started: make(chan struct{}), release: make(chan struct{})}
	session := NewSession(NewLoop(provider, map[string]ai.ChatProvider{"ollama": provider}, nil, retrievalService, tools.NewDispatcher(), nil))

	turnDone := make(chan struct{})
	go func() {
		defer close(turnDone)
		_, _ = session.Submit(context.Background(), "think about something slow", nil, nil)
	}()

	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("model call never started")
	}

	inspected := make(chan struct{})
	go func() {
		defer close(inspected)
		session.ModelInfo()
		session.PendingPlan()
		session.HasPendingActions()
	}()
	select {
	case <-inspected:
	case <-time.After(5 * time.Second):
		t.Fatal("session inspection blocked behind the running turn")
	}

	close(provider.release)
	select {
	case <-turnDone:
	case <-time.After(10 * time.Second):
		t.Fatal("turn did not finish after the model returned")
	}
}

// E-03: a successful write must not hide behind whatever finish_run wrote. A
// terse model — or a 2B model that says almost nothing — cannot suppress the
// record of what the vault actually did.
func TestTerseReplyStillReportsWhatTheVaultDid(t *testing.T) {
	created := ai.Action{Type: "create_note", Title: "Plan", Folder: "work"}
	failed := ai.Action{Type: "move_note", NoteID: 4, Folder: "archive"}
	outcome := agent.Outcome{
		Reply: "Done.",
		Ledger: []ai.ActionResult{
			{Action: created, Message: "created work/plan.md"},
			{Action: failed, Error: "destination folder does not exist"},
		},
	}

	reply := withExecutionLedger(outcome.Reply, outcome)
	if !strings.Contains(reply, "Done.") {
		t.Fatalf("the model's answer was dropped: %q", reply)
	}
	for _, want := range []string{"create_note", "created work/plan.md", "move_note", "destination folder does not exist", "failed"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply is missing %q: %s", want, reply)
		}
	}
}

// safeStop already writes its own record; appending a second one would show
// the same work twice.
func TestExecutionLedgerIsNotDuplicated(t *testing.T) {
	outcome := agent.Outcome{
		Reply:  "I stopped safely because the step budget was reached.\n\nVerified execution record:\n- create_note succeeded: created work/plan.md",
		Ledger: []ai.ActionResult{{Action: ai.Action{Type: "create_note", Title: "Plan"}, Message: "created work/plan.md"}},
	}
	reply := withExecutionLedger(outcome.Reply, outcome)
	if strings.Count(reply, executionLedgerHeading) != 1 {
		t.Fatalf("ledger heading appears %d times: %s", strings.Count(reply, executionLedgerHeading), reply)
	}
}

// A read-only turn changed nothing, so there is nothing to report.
func TestReadOnlyTurnHasNoLedger(t *testing.T) {
	outcome := agent.Outcome{Reply: "You have three notes in work."}
	if got := withExecutionLedger(outcome.Reply, outcome); got != outcome.Reply {
		t.Fatalf("read-only reply gained a ledger: %q", got)
	}
}

// scriptedSessionProvider replays one decision tool call per planning step and
// counts how many times Athena actually asked the model. Athena's decisions
// arrive as tool calls, so a script reproduces a real turn without a live
// model — and the call count is the only way to prove a path never reached one.
type scriptedSessionProvider struct {
	decisions []ai.ToolChatResult
	calls     int
}

func (p *scriptedSessionProvider) Name() string        { return "ChatGPT subscription" }
func (p *scriptedSessionProvider) ChatModel() string   { return "scripted" }
func (p *scriptedSessionProvider) SetChatModel(string) {}
func (p *scriptedSessionProvider) ChatModels(context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (p *scriptedSessionProvider) next() (ai.ToolChatResult, error) {
	p.calls++
	if p.calls > len(p.decisions) {
		return ai.ToolChatResult{}, errors.New("the session asked the model for an unscripted decision")
	}
	return p.decisions[p.calls-1], nil
}
func (p *scriptedSessionProvider) ChatWithToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ai.ToolChatResult, error) {
	return p.next()
}
func (p *scriptedSessionProvider) ChatWithRequiredToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ai.ToolChatResult, error) {
	return p.next()
}
func (p *scriptedSessionProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", errors.New("the session streamed prose instead of asking for a decision")
}

// I-01: the path the whole product rests on — the user asks for a change, the
// model proposes it, the vault performs it, the run finishes. What the user is
// shown must be the verified execution record, not the model's closing
// sentence: "Done." is a claim, the ledger line is evidence.
func TestSubmitAppliesAWriteAndReportsWhatTheVaultDid(t *testing.T) {
	_, retrievalService := doctorVault(t)

	applied := 0
	dispatcher := tools.NewDispatcher()
	dispatcher.Register("mark_done", func(context.Context, ai.Action) (string, error) {
		applied++
		return "marked note 1 done", nil
	})

	provider := &scriptedSessionProvider{decisions: []ai.ToolChatResult{
		decisionCall("propose_actions", `{"summary":"Mark the finished task done","actions":[{"type":"mark_done","note_id":1,"done":true}]}`),
		decisionCall("finish_run", `{"message":"Done."}`),
	}}
	session := NewSession(NewLoop(provider, map[string]ai.ChatProvider{"test": provider}, nil, retrievalService, dispatcher, nil))

	reply, err := session.Submit(context.Background(), "mark my chapter three task done", nil, nil)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if applied != 1 {
		t.Fatalf("the vault ran the approved write %d time(s), want exactly 1", applied)
	}
	if provider.calls != 2 {
		t.Fatalf("model calls = %d, want one to plan the write and one to finish", provider.calls)
	}
	for _, want := range []string{"Done.", executionLedgerHeading, "mark_done note:1 succeeded: marked note 1 done"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply is missing %q: %s", want, reply)
		}
	}
	ledger := session.LastLedger()
	if len(ledger) != 1 || ledger[0].Action != "mark_done" || ledger[0].Target != "note:1" || ledger[0].Status != "succeeded" {
		t.Fatalf("verified record = %+v", ledger)
	}
	if session.HasPendingActions() {
		t.Fatal("a finished run left a plan awaiting review")
	}
}

// I-01 / F-05: the approve-then-cancel path. Approval is the one human decision
// in the loop, so a cancelled apply must not silently discard it, must not
// replay work the ledger already verified, and must leave the user able to tell
// what state their vault is in.
func TestApprovedPlanCancelledMidApplyIsRecoverableExactlyOnce(t *testing.T) {
	_, retrievalService := doctorVault(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Actions without IDs run serially in the calling goroutine, so these counts
	// need no lock and note 2 is always reached after note 1.
	trashed := map[int64]int{}
	dispatcher := tools.NewDispatcher()
	dispatcher.Register("trash_note", func(_ context.Context, action ai.Action) (string, error) {
		trashed[action.NoteID]++
		if action.NoteID == 2 && trashed[2] == 1 {
			// Stands in for the user pressing Escape while their approved plan is
			// being applied: the turn context dies mid-batch.
			cancel()
			return "", errors.New("vault write interrupted")
		}
		return "moved the draft to .trash", nil
	})

	provider := &scriptedSessionProvider{decisions: []ai.ToolChatResult{
		decisionCall("propose_actions", `{"summary":"Delete both drafts","actions":[{"type":"trash_note","note_id":1},{"type":"trash_note","note_id":2}]}`),
		decisionCall("finish_run", `{"message":"Both drafts are gone."}`),
	}}
	session := NewSession(NewLoop(provider, map[string]ai.ChatProvider{"test": provider}, nil, retrievalService, dispatcher, nil))

	reply, err := session.Submit(ctx, "delete the two draft notes", nil, nil)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	staged := session.PendingPlan()
	if staged == nil || len(staged.Actions) != 2 {
		t.Fatalf("reply=%q staged plan=%+v, want both deletions held for review", reply, staged)
	}
	if !strings.Contains(reply, "no changes have been made") {
		t.Fatalf("the review text does not say the vault is untouched: %s", reply)
	}
	if len(trashed) != 0 || len(session.LastLedger()) != 0 {
		t.Fatalf("staging a plan already touched the vault: applied=%+v ledger=%+v", trashed, session.LastLedger())
	}

	if _, err = session.ApprovePlan(ctx, staged.ID); err == nil {
		t.Fatal("an apply that was cancelled halfway reported success")
	}
	restaged := session.PendingPlan()
	if restaged == nil {
		t.Fatal("the user's approval was lost when applying it was cancelled")
	}
	if restaged.ID == staged.ID {
		t.Fatal("the re-staged plan reused the single-use plan ID")
	}
	if len(restaged.Actions) != 1 || restaged.Actions[0].NoteID != 2 {
		t.Fatalf("re-staged actions = %+v, want only the deletion the ledger never verified", restaged.Actions)
	}
	// The user must be able to read where they stand out of the error alone.
	for _, want := range []string{staged.ID, restaged.ID, "/confirm", "/cancel"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error does not tell the user what state they are in (%q missing): %v", want, err)
		}
	}
	if _, err := session.ApprovePlan(context.Background(), staged.ID); err == nil {
		t.Fatal("the spent plan ID was accepted a second time")
	}

	reply, err = session.ApprovePlan(context.Background(), restaged.ID)
	if err != nil {
		t.Fatalf("confirming the re-staged plan: %v", err)
	}
	if trashed[1] != 1 {
		t.Fatalf("note 1 was deleted %d times; a verified action must never be replayed", trashed[1])
	}
	if trashed[2] != 2 {
		t.Fatalf("note 2 ran %d times, want the interrupted attempt plus the retry", trashed[2])
	}
	if session.HasPendingActions() {
		t.Fatal("the completed retry left a plan awaiting review")
	}
	if !strings.Contains(reply, "Both drafts are gone.") || strings.Count(reply, "trash_note note:1") != 1 {
		t.Fatalf("the final record is not an honest account of the two applies: %s", reply)
	}
}

// I-01: the listing shortcut answers "what notes do I have" from the vault
// inventory itself. Sending that to a model would be slower, cost tokens, and
// let a ~2B model paraphrase the user's own file list — so the model must not
// be called at all.
func TestListingShortcutAnswersFromInventoryWithoutTheModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	vault := t.TempDir()
	notes := storage.NewNoteStore(db)
	for _, note := range []*models.Note{
		{Title: "Grocery list", Path: filepath.Join(vault, "grocery-list.md"), Type: models.NoteTypeNote},
		{Title: "Reading plan", Path: filepath.Join(vault, "books", "reading-plan.md"), Type: models.NoteTypeNote},
	} {
		if _, err := notes.Create(note); err != nil {
			t.Fatalf("seed note %q: %v", note.Title, err)
		}
	}
	retrievalService := retrieval.NewService(vault, notes, storage.NewChunkStore(db), taskStateEmbedder{})

	provider := &scriptedSessionProvider{}
	session := NewSession(NewLoop(provider, map[string]ai.ChatProvider{"test": provider}, nil, retrievalService, tools.NewDispatcher(), nil))

	for _, phrasing := range []string{"what notes do i have?", "list my notes", "show my notes", "list notes", "show notes", "my notes"} {
		reply, err := session.Submit(context.Background(), phrasing, nil, nil)
		if err != nil {
			t.Fatalf("%q: %v", phrasing, err)
		}
		for _, want := range []string{"2 notes in your vault", "Grocery list — vault root", "Reading plan — books"} {
			if !strings.Contains(reply, want) {
				t.Fatalf("%q answered without %q: %s", phrasing, want, reply)
			}
		}
	}
	if provider.calls != 0 {
		t.Fatalf("the listing shortcut called the model %d time(s)", provider.calls)
	}
}
