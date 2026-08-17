package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
)

const maxActionsPerBatch = 32

type ToolKind string

const (
	ToolRead        ToolKind = "read"
	ToolWrite       ToolKind = "write"
	ToolDestructive ToolKind = "destructive"
)

// Policy is the single safety contract for a model-requested action. New
// actions must be added here before they can be considered production-ready.
type Policy struct {
	Kind      ToolKind
	Timeout   time.Duration
	RetrySafe bool
	// RequiresConfirmation is the declared floor, not the final answer. Read it
	// through PolicyFor, which also applies the create/move/rename default.
	RequiresConfirmation bool
	ParallelSafe         bool
	// AutoApproved is the allowlist: the one explicit, per-action opt-out from
	// the create/move/rename review default. Setting it says "this action can
	// never carry out an instruction injected into vault text". Nothing
	// qualifies today, so nothing sets it.
	AutoApproved bool
}

// reviewedActionPrefixes covers every action family that creates, moves, or
// renames vault content. Vault text is sent to a remote provider, so a note
// holding injected instructions can steer the model's plan; an unreviewed
// create/move/rename would then execute that injection against the vault. The
// default is deliberately a prefix rather than a fixed list of action names so
// a create_/move_/rename_ action added later is reviewed without anyone
// remembering to opt in. An action escapes only via AutoApproved on its row.
var reviewedActionPrefixes = []string{"create_", "move_", "rename_"}

func defaultsToReview(actionType string) bool {
	for _, prefix := range reviewedActionPrefixes {
		if strings.HasPrefix(actionType, prefix) {
			return true
		}
	}
	return false
}

// A RequiresConfirmation set here marks an action reviewed for a reason of its
// own. create_/move_/rename_ rows are reviewed regardless via PolicyFor, so
// reading this table alone understates which actions pause.
var actionPolicies = map[string]Policy{
	"list_folders":  {Kind: ToolRead, Timeout: 10 * time.Second, RetrySafe: true, ParallelSafe: true},
	"folder_exists": {Kind: ToolRead, Timeout: 10 * time.Second, RetrySafe: true, ParallelSafe: true},

	"create_note":    {Kind: ToolWrite, Timeout: time.Minute},
	"create_task":    {Kind: ToolWrite, Timeout: time.Minute},
	"ensure_folders": {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"move_note":      {Kind: ToolWrite, Timeout: time.Minute},
	// S-01 again, and the line this table draws: an action is reviewed when the
	// MODEL'S OWN TEXT lands in a note body — the prose the user reads as their
	// own writing. append_note and replace_section put action.Content straight
	// into the .md file, exactly as update_note does, so an instruction injected
	// into one note can write itself into another. That is the whole threat, and
	// it does not care that neither name starts with create_/move_/rename_.
	//
	// The flag-and-setting writes stay automatic on purpose: reviewing them would
	// put a plan card in front of the routine half of a ~2B-model session while
	// stopping no injection. mark_done flips done:; finish_book stamps Athena's
	// clock, never model text; update_book_metadata fills an empty authors/genres
	// field and refuses to replace one that is already set; set_folder_colors
	// without a color and set_graph_node_size touch .obsidian/graph.json, an
	// Obsidian display setting rather than a note.
	"append_note":     {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"replace_section": {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"mark_done":       {Kind: ToolWrite, Timeout: time.Minute},
	"create_folder":   {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"link_folders":    {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"unlink_folders":  {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	// Without a color this only fills missing Athena-owned visual groups in
	// .obsidian/graph.json, which SyncFolderGraph already writes after structural
	// changes, so it is safe to apply directly. Carrying a color makes it an
	// overwrite, which requiresContentReview catches.
	"set_folder_colors":   {Kind: ToolWrite, Timeout: time.Minute},
	"set_graph_node_size": {Kind: ToolWrite, Timeout: time.Minute},
	// G-03. "Add X to the graph" is a directory, a new Athena-managed index note
	// and an orb color in one action, so it creates vault content and falls under
	// S-01: an instruction injected into vault text must not be able to grow the
	// tree unreviewed. The create_ prefix happens to catch this name, but the row
	// says so anyway - the four rows above exist because a name stopped matching
	// what the action does, and renaming this to add_folder_to_graph must not
	// silently drop the plan card.
	//
	// Not RetrySafe: the folder, the index note and .obsidian/graph.json are three
	// separate writes with a partial rollback, so a second attempt after a timeout
	// would run against half-applied state rather than a clean vault. Not
	// ParallelSafe: graph.json is one shared file, and two concurrent adds
	// read-modify-write it, so the second would drop the first folder's group.
	"create_graph_folder": {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"rename_note":         {Kind: ToolWrite, Timeout: time.Minute},
	// These four are create/move operations that the create_/move_/rename_ name
	// rule misses, because they are named for the intent rather than the file
	// operation. duplicate_note writes a new note file; archive, unarchive and
	// restore each relocate a note between folders exactly as move_note does.
	// S-01's premise is that an unreviewed file mutation driven by a remote
	// model is the hole, so what the action DOES has to decide, not how it is
	// spelled.
	"duplicate_note":       {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"restore_note":         {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"archive_note":         {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"unarchive_note":       {Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: true},
	"create_book":          {Kind: ToolWrite, Timeout: time.Minute},
	"update_book_metadata": {Kind: ToolWrite, Timeout: time.Minute},
	"finish_book":          {Kind: ToolWrite, Timeout: time.Minute},

	"update_note":   {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
	"trash_note":    {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
	"delete_folder": {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
	"rename_folder": {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
	"move_folder":   {Kind: ToolDestructive, Timeout: time.Minute, RequiresConfirmation: true},
}

// PolicyFor returns the effective policy for a built-in action. The boolean is
// false for an extension that has not yet declared its safety contract. This is
// the only place the create/move/rename review default is applied, so callers
// and the dispatcher cannot disagree about whether an action needs a plan card.
func PolicyFor(actionType string) (Policy, bool) {
	actionType = strings.TrimSpace(actionType)
	policy, ok := actionPolicies[actionType]
	if !ok {
		return Policy{}, false
	}
	if defaultsToReview(actionType) && !policy.AutoApproved {
		policy.RequiresConfirmation = true
	}
	return policy, true
}

func policyFor(actionType string) Policy {
	if policy, ok := PolicyFor(actionType); ok {
		return policy
	}
	// Custom handlers used by callers/tests default to the safest behavior:
	// a bounded, non-retriable, serial write. An unregistered create/move/rename
	// still waits for review — it has no policy row that could allowlist it.
	actionType = strings.TrimSpace(actionType)
	return Policy{Kind: ToolWrite, Timeout: time.Minute, RequiresConfirmation: defaultsToReview(actionType)}
}

// requiresContentReview covers the review decisions the action type alone
// cannot answer. PolicyFor stays keyed by type because the dispatcher reads the
// same row for timeout, retry and parallel safety, where the payload is
// irrelevant; only the review question depends on what the action carries, so
// RequiresConfirmation asks it here.
//
// set_folder_colors with no color only fills a gap: notes.syncFolderColors keeps
// any valid existing group (G-04). An explicit color replaces that group,
// including one the user picked in Obsidian, and vault text reaching a remote
// model could carry an injected instruction to restyle the graph — so a
// deliberate overwrite of the user's own setting gets a plan card.
func requiresContentReview(action ai.Action) bool {
	return strings.TrimSpace(action.Type) == "set_folder_colors" && strings.TrimSpace(action.Color) != ""
}

// RequiresConfirmation keeps review policy out of UI/session code. Any batch
// containing a write is considered broad enough to review as one plan.
//
// The len > 1 rule is breadth only, and it is the one review reason that can
// disappear: agent.Runner.prepareActions drops actions it has already executed
// and verified, so a reviewed pair can reach RequiresApproval as a lone
// survivor. That is safe only because every other reason here is per-action —
// the policy row, the create/move/rename default, requiresContentReview — and
// therefore survives the batch shrinking. So no action may rely on a sibling
// for its plan card: an action that mutates a note body declares
// RequiresConfirmation on its own row, which is what append_note and
// replace_section were missing. Adding a body-writing action without that flag
// re-opens the hole even though the multi-action rule appears to cover it.
func RequiresConfirmation(actions []ai.Action) bool {
	if len(actions) > 1 {
		for _, action := range actions {
			if policyFor(action.Type).Kind != ToolRead {
				return true
			}
		}
	}
	for _, action := range actions {
		if policyFor(action.Type).RequiresConfirmation || requiresContentReview(action) {
			return true
		}
	}
	return false
}

// validateAction is the dispatcher boundary for model-supplied data. Note
// services still validate paths and filesystem rules; this layer rejects bad
// shapes before any handler can begin side effects.
func validateAction(action ai.Action, known bool) error {
	action.Type = strings.TrimSpace(action.Type)
	if action.Type == "" {
		return fmt.Errorf("action type is required")
	}
	if !known {
		return fmt.Errorf("unknown action type %q", action.Type)
	}

	switch action.Type {
	case "create_note", "create_task", "create_book":
		if strings.TrimSpace(action.Title) == "" {
			return fmt.Errorf("%s requires title", action.Type)
		}
		if err := validateFolderValue(action.Type+" folder", action.Folder); err != nil {
			return err
		}
	case "ensure_folders":
		if len(action.Paths) == 0 {
			return fmt.Errorf("ensure_folders requires paths")
		}
		for index, path := range action.Paths {
			if err := validateFolderValue(fmt.Sprintf("ensure_folders paths[%d]", index), path); err != nil {
				return err
			}
		}
	case "link_folders", "unlink_folders":
		if len(action.Folders) < 2 {
			return fmt.Errorf("%s requires at least two folders", action.Type)
		}
		for index, folder := range action.Folders {
			if err := validateFolderValue(fmt.Sprintf("%s folders[%d]", action.Type, index), folder); err != nil {
				return err
			}
		}
	case "move_note", "update_note", "append_note", "replace_section", "mark_done", "rename_note", "duplicate_note", "trash_note", "restore_note", "archive_note", "unarchive_note", "finish_book", "update_book_metadata":
		if action.NoteID <= 0 {
			return fmt.Errorf("%s requires note_id", action.Type)
		}
		if action.Type == "move_note" || action.Type == "duplicate_note" {
			if err := validateFolderValue(action.Type+" folder", action.Folder); err != nil {
				return err
			}
		}
	case "create_folder", "folder_exists", "delete_folder", "rename_folder", "move_folder", "set_folder_colors", "create_graph_folder":
		if strings.TrimSpace(action.Folder) == "" {
			return fmt.Errorf("%s requires folder", action.Type)
		}
		if err := validateFolderValue(action.Type+" folder", action.Folder); err != nil {
			return err
		}
		if action.Type == "move_folder" {
			if err := validateFolderValue("move_folder new_folder", action.NewFolder); err != nil {
				return err
			}
		}
	}
	if action.Type == "update_book_metadata" && len(action.Authors) == 0 && len(action.Genres) == 0 {
		return fmt.Errorf("update_book_metadata requires authors or genres")
	}

	switch action.Type {
	case "rename_folder":
		if strings.TrimSpace(action.NewFolder) == "" {
			return fmt.Errorf("%s requires new_folder", action.Type)
		}
	}
	if action.Type == "append_note" && strings.TrimSpace(action.Content) == "" {
		return fmt.Errorf("append_note requires content")
	}
	if action.Type == "replace_section" {
		if strings.TrimSpace(action.Section) == "" || strings.TrimSpace(action.ExpectedContent) == "" {
			return fmt.Errorf("replace_section requires section and expected_content")
		}
	}
	if action.Type == "set_graph_node_size" && (action.NodeSizeMultiplier < 0.25 || action.NodeSizeMultiplier > 3) {
		return fmt.Errorf("set_graph_node_size requires node_size_multiplier between 0.25 and 3")
	}
	// An empty color is the normal case: it asks Athena to pick one. A supplied
	// value has to be a real color, or Obsidian silently drops the group.
	// create_graph_folder hands its Color to the same AddFolderGraphColors, and
	// rejecting it here means the directory is never created and rolled back.
	if (action.Type == "set_folder_colors" || action.Type == "create_graph_folder") && strings.TrimSpace(action.Color) != "" {
		if !validHexColor(action.Color) {
			return fmt.Errorf("set_folder_colors color must be a #RRGGBB hex value, got %q", action.Color)
		}
	}
	return nil
}

func validHexColor(value string) bool {
	clean := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(clean) != 6 {
		return false
	}
	for _, r := range clean {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// validateFolderValue prevents a common weak-model failure where a note file
// path is put into a folder field. CleanFolder intentionally accepts any safe
// relative path, including names ending in .md, so this semantic check belongs
// at the model-action boundary rather than in the general filesystem utility.
func validateFolderValue(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	normalized := strings.TrimRight(strings.ReplaceAll(value, "\\", "/"), "/")
	if strings.HasSuffix(strings.ToLower(normalized), ".md") {
		return fmt.Errorf("%s must be a folder path, not a note file path %q", field, value)
	}
	return nil
}
