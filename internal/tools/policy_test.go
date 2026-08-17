package tools

import (
	"context"
	"testing"

	"github.com/tiredbooy/internal/ai"
)

// S-01: vault text reaches a remote provider, so a note carrying injected
// instructions can steer a plan. Every create/move/rename must stop at a plan
// card, including as the only action in a turn, where nothing else forces
// review.
func TestCreateMoveRenameRequireReviewAlone(t *testing.T) {
	reviewed := []string{
		"create_note", "create_task", "create_book", "create_folder", "ensure_folders",
		"move_note", "move_folder", "rename_note", "rename_folder",
	}
	for _, actionType := range reviewed {
		policy, ok := PolicyFor(actionType)
		if !ok {
			t.Fatalf("%s has no declared policy", actionType)
		}
		if !policy.RequiresConfirmation {
			t.Errorf("PolicyFor(%q).RequiresConfirmation = false, want true", actionType)
		}
		if !RequiresConfirmation([]ai.Action{{Type: actionType}}) {
			t.Errorf("single %s action ran without review", actionType)
		}
	}
}

// G-03's create_graph_folder creates a folder and writes a new index note into
// the vault, so S-01 puts it with the reviewed writes. It reached the dispatcher
// with no row at all, which left its timeout, retry and parallel safety to the
// unknown-handler fallback rather than to a declared contract.
func TestCreateGraphFolderRequiresReview(t *testing.T) {
	policy, ok := PolicyFor("create_graph_folder")
	if !ok {
		t.Fatal("create_graph_folder is registered in the dispatcher but has no declared policy")
	}
	if !policy.RequiresConfirmation {
		t.Error("create_graph_folder creates a folder and a note but runs with no plan card")
	}
	if !RequiresConfirmation([]ai.Action{{Type: "create_graph_folder", Folder: "projects"}}) {
		t.Error("single create_graph_folder action ran without review")
	}
	// It writes .obsidian/graph.json and the folder tree, which two concurrent
	// adds would race, and it is three writes deep with a partial rollback, so a
	// retry after a timeout would resume from half-applied state.
	if policy.ParallelSafe || policy.RetrySafe {
		t.Errorf("create_graph_folder = %+v, want neither ParallelSafe nor RetrySafe", policy)
	}
}

// The default is a prefix rule, not a fixed list, so an action added later
// cannot slip through by nobody remembering to flag it. This covers both a
// registered-but-unlisted name and the unknown-handler fallback.
func TestUnlistedCreateMoveRenameStillRequiresReview(t *testing.T) {
	for _, actionType := range []string{"create_diagram", "move_book", "rename_task"} {
		if _, ok := PolicyFor(actionType); ok {
			t.Fatalf("%s unexpectedly has a policy row; pick a name that has none", actionType)
		}
		if !RequiresConfirmation([]ai.Action{{Type: actionType}}) {
			t.Errorf("unknown action %q ran without review", actionType)
		}
	}
}

// AutoApproved is the whole escape hatch. If it stops working the allowlist is
// silently gone and the only way back to an automatic write is deleting the
// default, which is the mistake this guards.
func TestAutoApprovedOptsOutOfDefaultReview(t *testing.T) {
	const actionType = "create_note"
	original, ok := actionPolicies[actionType]
	if !ok {
		t.Fatalf("%s has no declared policy", actionType)
	}
	t.Cleanup(func() { actionPolicies[actionType] = original })

	allowed := original
	allowed.RequiresConfirmation = false
	allowed.AutoApproved = true
	actionPolicies[actionType] = allowed

	if RequiresConfirmation([]ai.Action{{Type: actionType}}) {
		t.Error("AutoApproved action still required review")
	}
}

// Reviewing every write would make a 2B-model session unusable. Flag flips,
// clock stamps, gap-filling metadata and reads carry no model prose into a
// note, so they must stay automatic when they are the only action in a turn.
func TestNonCreateWritesStayAutomatic(t *testing.T) {
	for _, actionType := range []string{"mark_done", "finish_book", "update_book_metadata", "set_folder_colors", "set_graph_node_size", "list_folders"} {
		if RequiresConfirmation([]ai.Action{{Type: actionType}}) {
			t.Errorf("single %s action now pauses for review, which S-01 did not ask for", actionType)
		}
	}
}

// G-02 gave set_folder_colors an explicit color, which replaces an existing
// group instead of filling a gap - including a color the user chose in
// Obsidian. Overwriting the user's own setting must reach a plan card; filling
// a gap must stay automatic.
func TestExplicitFolderColorRequiresReview(t *testing.T) {
	if !RequiresConfirmation([]ai.Action{{Type: "set_folder_colors", Folder: "projects", Color: "#4C6EF5"}}) {
		t.Error("explicit set_folder_colors color overwrote graph settings without review")
	}
	if RequiresConfirmation([]ai.Action{{Type: "set_folder_colors", Folder: "projects", Color: "   "}}) {
		t.Error("blank color is still the gap-filling default and must not pause for review")
	}
}

// The create_/move_/rename_ prefix rule is a name heuristic, and these four
// actions are named for intent rather than for the file operation they perform.
// S-01 is about unreviewed file mutations driven by a remote model, so what the
// action does to the vault has to decide — not how it happens to be spelled.
func TestFileRelocatingActionsRequireReviewDespiteTheirNames(t *testing.T) {
	for _, actionType := range []string{"duplicate_note", "restore_note", "archive_note", "unarchive_note"} {
		if defaultsToReview(actionType) {
			t.Fatalf("%s now matches the prefix rule; this test guards the case where it does not", actionType)
		}
		if !RequiresConfirmation([]ai.Action{{Type: actionType, NoteID: 1}}) {
			t.Errorf("%s writes or relocates a note file but runs with no plan card", actionType)
		}
	}

	// Actions that only flip a flag or stamp Athena's own clock stay automatic;
	// this guards against the fix sliding into "review everything", which would
	// make a 2B-model session unusable.
	for _, actionType := range []string{"mark_done", "finish_book"} {
		if RequiresConfirmation([]ai.Action{{Type: actionType, NoteID: 1}}) {
			t.Errorf("%s edits in place and should not have become review-required", actionType)
		}
	}
}

// S-01's actual hole: append_note and replace_section write the model's own
// text into a note's .md body, exactly as the reviewed update_note does, but
// neither name matches the create_/move_/rename_ prefix rule. A single-action
// plan therefore reached a real dispatcher and grew the note file with no plan
// card, which is precisely how an instruction injected into vault text gets
// itself written into the vault.
func TestNoteBodyWritesRequireReviewAlone(t *testing.T) {
	for _, action := range []ai.Action{
		{Type: "append_note", NoteID: 1, Content: "injected line"},
		{Type: "replace_section", NoteID: 1, Section: "Notes", ExpectedContent: "old", Content: "injected line"},
	} {
		if !RequiresConfirmation([]ai.Action{action}) {
			t.Errorf("single %s wrote model text into a note file with no plan card", action.Type)
		}
	}
}

// agent.Runner.prepareActions drops actions it has already executed and
// verified before RequiresApproval is consulted, so a reviewed batch can arrive
// as a lone survivor. The multi-action rule is breadth only and legitimately
// stops applying; what must not happen is a file mutation losing its plan card
// because its sibling was the only reason the pair was reviewed.
func TestReviewSurvivesBatchShrinkingToOneAction(t *testing.T) {
	appendNote := ai.Action{Type: "append_note", NoteID: 1, Content: "injected line"}
	batch := []ai.Action{{Type: "create_folder", Folder: "projects"}, appendNote}
	if !RequiresConfirmation(batch) {
		t.Fatal("multi-action write batch ran without review")
	}
	if !RequiresConfirmation([]ai.Action{appendNote}) {
		t.Error("append_note lost its plan card once create_folder was recorded as succeeded")
	}
}

// Every other write action rejects a missing required field before a handler can
// start side effects. create_graph_folder had no case at all, so an action block
// naming it with no fields whatsoever passed the dispatcher boundary and only
// the handler's own empty check stopped it.
func TestCreateGraphFolderValidatesItsFolder(t *testing.T) {
	d := NewDispatcher()
	d.Register("create_graph_folder", func(context.Context, ai.Action) (string, error) { return "", nil })

	if err := d.Validate([]ai.Action{{Type: "create_graph_folder"}}); err == nil {
		t.Error("create_graph_folder with no fields at all passed validation")
	}
	if err := d.Validate([]ai.Action{{Type: "create_graph_folder", Folder: "projects/plan.md"}}); err == nil {
		t.Error("create_graph_folder accepted a note file path as its folder")
	}
	// Same Color field, same AddFolderGraphColors, so the same hex rule applies;
	// rejecting it here means no directory is created and then rolled back.
	if err := d.Validate([]ai.Action{{Type: "create_graph_folder", Folder: "projects", Color: "blue"}}); err == nil {
		t.Error("create_graph_folder accepted a color Obsidian cannot render")
	}
	if err := d.Validate([]ai.Action{{Type: "create_graph_folder", Folder: "projects", Color: "#4C6EF5"}}); err != nil {
		t.Errorf("valid create_graph_folder rejected: %v", err)
	}
}
