package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/tools"
)

// R-06: a ~2B model reads the system prompt plus this contract and nothing
// else. If an action's name and fields still appear in the prompt, "narrowing"
// only moved text around — the model still sees all twenty-five actions.
func TestNarrowedContractIsTheOnlyActionCatalogTheModelSees(t *testing.T) {
	goal := "add a journal note about today's ideas"
	advertised := actionTypesForGoal(goal)
	if len(advertised) >= len(mutationActionTypes) {
		t.Fatalf("goal %q advertised %d of %d actions; expected a narrowed set", goal, len(advertised), len(mutationActionTypes))
	}
	contract := taskActionContractMessage(advertised).Content

	allowed := make(map[string]bool, len(advertised))
	for _, actionType := range advertised {
		allowed[actionType] = true
		if !strings.Contains(contract, actionType) {
			t.Fatalf("contract omits advertised action %s: %s", actionType, contract)
		}
	}
	for _, actionType := range mutationActionTypes {
		if allowed[actionType] {
			continue
		}
		if strings.Contains(contract, actionType) {
			t.Fatalf("contract leaked unadvertised action %s", actionType)
		}
		// The prompt is sent on every turn, including the no-native-tools
		// fallback where it is the model's only other instruction.
		if strings.Contains(ai.SystemPrompt, actionType) {
			t.Fatalf("system prompt still catalogs %s, so the narrowed contract is not the only action list", actionType)
		}
	}
}

// The contract replaced the prompt's catalog, so it must carry the fields the
// catalog carried: required ones to pass validation, optional ones so a model
// told only "create_note requires title" does not write an empty, unfiled note.
func TestNarrowedContractCarriesRequiredAndOptionalFields(t *testing.T) {
	contract := taskActionContractMessage([]string{"create_note", "move_note", "update_book_metadata"}).Content
	for _, want := range []string{
		"create_note requires title; optional content, tags, folder",
		"move_note requires note_id, folder",
		"update_book_metadata requires note_id plus authors or genres",
		"— any user content",
	} {
		if !strings.Contains(contract, want) {
			t.Fatalf("contract missing %q: %s", want, contract)
		}
	}
}

func TestNarrowedContractIsMateriallySmallerThanTheFullOne(t *testing.T) {
	narrow := len(taskActionContractMessage(actionTypesForGoal("add a journal note about today's ideas")).Content)
	full := len(taskActionContractMessage(mutationActionTypes).Content)
	if narrow*2 > full {
		t.Fatalf("narrowed contract is %d chars against a full %d; expected less than half", narrow, full)
	}
	if len(ai.SystemPrompt) > 7000 {
		t.Fatalf("system prompt is %d chars; it is the fixed cost of every 2B turn and must stay small", len(ai.SystemPrompt))
	}
}

// G-01: users say "orb", not "set_folder_colors". A goal that names the graph in
// the user's own words has to arrive as a short graph contract; before this,
// "make the work orb better" matched no branch and got all twenty-five actions,
// and "style this folder" got the folder branch without set_graph_node_size.
func TestGraphPhrasingsAdvertiseTheGraphActions(t *testing.T) {
	for _, goal := range []string{
		"make the work orb better",
		"make the work orb blue",
		"make my projects folder stand out",
		"style this folder",
		"add reading to the graph",
		"bigger orbs please",
	} {
		advertised := actionTypesForGoal(goal)
		joined := strings.Join(advertised, ",")
		for _, want := range []string{"set_folder_colors", "set_graph_node_size"} {
			if !strings.Contains(joined, want) {
				t.Errorf("goal %q advertised %v; missing %s", goal, advertised, want)
			}
		}
		// Falling through to the full contract is the other way to lose a graph
		// request: the action is technically listed, buried in twenty-five.
		if len(advertised) >= len(mutationActionTypes) {
			t.Errorf("goal %q advertised all %d actions; a graph goal must be narrowed", goal, len(advertised))
		}
	}

	// "make the work orb blue" is only useful if the model is told color exists.
	contract := taskActionContractMessage(actionTypesForGoal("make the work orb blue")).Content
	if !strings.Contains(contract, "set_folder_colors requires folder; optional include_children, color") {
		t.Fatalf("contract does not advertise the optional color field: %s", contract)
	}
	if !strings.Contains(contract, "orb") {
		t.Fatalf("contract never uses the user's word for a graph node: %s", contract)
	}
}

// G-03's create_graph_folder is dispatchable, so both paths the model can reach
// it by have to name it: the prose contract a no-native-tools Ollama model reads
// and the propose_actions JSON Schema. R-06 deleted the prompt catalog, so an
// action missing from here is one the model can only produce by accident, while
// ai.ExtractActions still parses it out of a fenced block.
func TestGraphGoalAdvertisesCreateGraphFolder(t *testing.T) {
	for _, goal := range []string{
		"add my projects to the graph",
		"add reading to the graph",
		"make the work orb blue",
	} {
		advertised := actionTypesForGoal(goal)
		joined := strings.Join(advertised, ",")
		if !strings.Contains(joined, "create_graph_folder") {
			t.Errorf("goal %q advertised %v; missing create_graph_folder", goal, advertised)
		}
	}

	// The model needs the field names and the purpose, not just the name.
	contract := taskActionContractMessage(actionTypesForGoal("add my projects to the graph")).Content
	if !strings.Contains(contract, "create_graph_folder requires folder; optional color") {
		t.Fatalf("contract does not advertise create_graph_folder's fields: %s", contract)
	}
	if !strings.Contains(contract, "create_graph_folder requires folder; optional color — ") {
		t.Fatalf("contract advertises create_graph_folder with no purpose clause: %s", contract)
	}

	// The native-tools path reads the schema, not the prose.
	variants, _ := proposalActionSchema(actionTypesForGoal("add my projects to the graph"))["oneOf"].([]any)
	found := false
	for _, variant := range variants {
		schema, _ := variant.(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		typeSchema, _ := properties["type"].(map[string]any)
		names, _ := typeSchema["enum"].([]string)
		if len(names) == 1 && names[0] == "create_graph_folder" {
			found = true
			if _, ok := properties["folder"]; !ok {
				t.Fatal("create_graph_folder schema has no folder property")
			}
		}
	}
	if !found {
		t.Fatalf("propose_actions schema has no create_graph_folder variant (%d variants)", len(variants))
	}
}

// The other direction: widening graph intent must not hand vault-graph
// mutations to goals that merely contain those letters.
func TestNonGraphGoalsDoNotAdvertiseGraphActions(t *testing.T) {
	for _, goal := range []string{
		"save a note about heat absorption",
		"write a journal entry about my lifestyle",
		"create a task to call the dentist",
		"finish the book I was reading",
		"organize my notes into folders",
	} {
		// set_graph_node_size and create_graph_folder are the graph branch's unique
		// signals: the folder branch legitimately offers set_folder_colors for
		// "organize" goals, and such a goal wants create_folder, not an orb.
		joined := strings.Join(actionTypesForGoal(goal), ",")
		for _, unwanted := range []string{"set_graph_node_size", "create_graph_folder"} {
			if strings.Contains(joined, unwanted) {
				t.Errorf("goal %q advertised %s: %s", goal, unwanted, joined)
			}
		}
	}
}

// Narrowing what the model is TOLD must never widen what the dispatcher
// ACCEPTS. internal/tools validates every action independently of the goal, so
// an action this goal never advertised is still gated there.
func TestDispatcherStillFailsClosedOnUnadvertisedActions(t *testing.T) {
	advertised := actionTypesForGoal("add a journal note about today's ideas")
	for _, actionType := range advertised {
		if actionType == "set_graph_node_size" || actionType == "trash_note" {
			t.Fatalf("goal unexpectedly advertised %s; pick another unadvertised action for this test", actionType)
		}
	}

	dispatcher := tools.NewDispatcher()
	dispatcher.Register("trash_note", func(context.Context, ai.Action) (string, error) { return "", nil })

	// Unregistered and unadvertised: rejected as an unknown type.
	if err := dispatcher.Validate([]ai.Action{{Type: "set_graph_node_size", NodeSizeMultiplier: 2}}); err == nil {
		t.Fatal("dispatcher accepted an action with no registered handler")
	}
	// Registered but unadvertised: still shape-checked, not waved through.
	if err := dispatcher.Validate([]ai.Action{{Type: "trash_note"}}); err == nil {
		t.Fatal("dispatcher accepted trash_note without a note_id")
	}
	if err := dispatcher.Validate([]ai.Action{{Type: "trash_note", NoteID: 7}}); err != nil {
		t.Fatalf("dispatcher rejected a valid registered action: %v", err)
	}
}
