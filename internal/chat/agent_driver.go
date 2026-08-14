package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/tiredbooy/internal/agent"
	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/tools"
)

// sessionAgentDriver adapts the chat application's model, retrieval, and tool
// services to the generic bounded agent runner. It contains no loop policy;
// the runner owns continuation, budgets, repeat detection, and safe stopping.
type sessionAgentDriver struct {
	session *Session
}

func agentRunContractMessage() models.Message {
	return models.Message{Role: "system", Content: `[ATHENA AGENT RUN CONTRACT]
Choose exactly one next decision for the original user goal:
- Read/search when exact current vault facts are still missing.
- Propose the smallest necessary action plan. When propose_actions is available, a mutation decision MUST call it with a non-empty actions array. Do not describe intended changes in prose without that tool call. Use fenced action JSON only when propose_actions is unavailable.
- Finish only when the goal is answered or verified execution observations prove every requested change succeeded. When finish_run is available, use that tool for the final user-facing answer.
- Ask one specific question only when the missing information cannot be resolved from inventory or read tools. When request_clarification is available, use that tool instead of returning the question as prose.
- Once the user's clarification supplies the missing facts, propose the complete reviewable action plan immediately. Do not ask for permission in prose; Athena's pending-plan review is the permission boundary.
Execution observations are authoritative. Never repeat a succeeded action, claim an unexecuted change, or hide a failed action. Do not expose private reasoning.
[END AGENT RUN CONTRACT]`}
}

func (d sessionAgentDriver) Decide(ctx context.Context, state *agent.RunState, emit agent.EventSink) (agent.Decision, error) {
	status := func(message string) {
		if emit == nil {
			return
		}
		activity := d.session.activityEvent(message)
		emit(agent.Event{
			RunID: state.ID, Step: state.Step, Phase: agent.Phase(activity.Phase),
			Message: activity.Message, Provider: activity.Provider, Model: activity.Model,
			Target: activity.Path,
		})
	}
	actionTypes := actionTypesForGoal(state.Goal)
	decisionMessages := state.Messages
	if !hasTaskActionContract(decisionMessages) {
		decisionMessages = append(append([]models.Message(nil), decisionMessages...), taskActionContractMessage(actionTypes))
	}
	result, err := d.session.runReadToolLoopStateWithPolicy(ctx, decisionMessages, status, state.ExpectedAction, actionTypes)
	if err != nil {
		return agent.Decision{}, err
	}
	state.Messages = result.Messages
	cleaned, actions := ai.ExtractActions(result.Content)
	if len(actions) > 0 {
		actions = normalizeTrackedBookActions(state.Goal, actions)
		actions = d.session.refineNoteTitles(ctx, state.Goal, actions, status)
		return agent.Decision{Kind: agent.DecisionAct, Message: cleaned, Raw: result.Content, Actions: actions}, nil
	}
	kind := agent.DecisionFinish
	if result.DecisionTool == "request_clarification" || (result.DecisionTool == "" && looksLikeClarifyingQuestion(cleaned)) {
		kind = agent.DecisionAskUser
	}
	return agent.Decision{Kind: kind, Message: cleaned, Raw: result.Content}, nil
}

// normalizeTrackedBookActions keeps a weak local planner on the book domain
// path when the user explicitly describes reading lifecycle state. create_book
// owns factual metadata and trustworthy start timestamps; create_note does not.
func normalizeTrackedBookActions(goal string, actions []ai.Action) []ai.Action {
	lowerGoal := strings.ToLower(goal)
	trackedRequest := strings.Contains(lowerGoal, "book") && containsAny(lowerGoal, []string{
		"started reading", "start reading", "currently reading", "i am reading", "i'm reading",
	})
	if !trackedRequest {
		return actions
	}
	normalized := append([]ai.Action(nil), actions...)
	seen := make(map[string]bool)
	result := normalized[:0]
	for _, action := range normalized {
		if action.Type == "create_note" && strings.Contains(strings.ToLower(action.Folder), "book") {
			action.Type = "create_book"
			action.Content = ""
			action.Tags = nil
		}
		if action.Type == "create_book" {
			key := strings.ToLower(strings.Join(strings.Fields(action.Title), " ")) + "\x00" + action.Folder
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		result = append(result, action)
	}
	return result
}

func (d sessionAgentDriver) Validate(state *agent.RunState, decision agent.Decision) error {
	if decision.Kind == agent.DecisionAct {
		if len(decision.Actions) == 0 {
			return fmt.Errorf("the model selected an action decision without any executable actions")
		}
		if d.session.loop.dispatcher == nil {
			return fmt.Errorf("vault action execution is unavailable")
		}
		if err := d.session.loop.dispatcher.Validate(decision.Actions); err != nil {
			d.session.loop.dispatcher.RecordRejectedPlan(decision.Actions, err)
			return fmt.Errorf("the proposed action plan failed validation: %w", err)
		}
		// Folder creation always enters application-owned review under the current
		// tool policy. Let that exact-path preview collect permission instead of
		// forcing the model to ask a redundant prose question. Keep the semantic
		// guard active if a future policy ever makes such a proposal unreviewed.
		if warning := implicitFolderCreationWarning(state.Goal, decision.Actions); warning != "" && !tools.RequiresConfirmation(decision.Actions) {
			return fmt.Errorf("%s Inspect the existing folder tree and choose a plan that stays within the user's request", warning)
		}
		return nil
	}
	if state.ContextSupplied && asksUserForVaultInventory(decision.Message) {
		return fmt.Errorf("the complete vault inventory was already supplied; use it instead of asking the user to provide it")
	}
	if decision.Kind == agent.DecisionAskUser && state.ExpectedAction && asksForActionPermission(decision.Message) {
		return fmt.Errorf("do not ask for a second prose confirmation; propose the complete action plan so Athena's application-owned review can request permission")
	}
	if state.ExpectedAction && len(state.Completed) == 0 && claimsAction(decision.Raw) {
		return fmt.Errorf("the reply claimed a vault change but did not provide executable action data")
	}
	return nil
}

func asksForActionPermission(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	permission := containsAny(lower, []string{"may i", "should i", "would you like me to", "do you want me to", "shall i", "can i"})
	mutation := containsAny(lower, []string{"create", "make", "move", "update", "rename", "delete", "remove", "archive", "restore", "organize", "organise", "link", "unlink"})
	return permission && mutation
}

func (d sessionAgentDriver) RequiresApproval(actions []ai.Action) bool {
	return tools.RequiresConfirmation(actions)
}

func (d sessionAgentDriver) Execute(ctx context.Context, state *agent.RunState, actions []ai.Action, emit agent.EventSink) []ai.ActionResult {
	if d.session.loop.dispatcher == nil {
		results := make([]ai.ActionResult, len(actions))
		for index, action := range actions {
			err := fmt.Errorf("vault action execution is unavailable")
			results[index] = ai.ActionResult{Action: action, Error: err.Error(), Err: err}
		}
		return results
	}
	progressCtx := tools.WithActionProgress(ctx, func(progress tools.ActionProgress) {
		if emit == nil {
			return
		}
		emit(agent.Event{
			RunID: state.ID, Step: state.Step, Phase: agent.PhaseExecuting,
			Message: actionProgressMessage(progress), Tool: progress.Action.Type,
			Target: chatActionTarget(progress.Action), State: progress.State,
		})
	})
	return d.session.loop.dispatcher.RunBatch(progressCtx, actions, 4)
}

func looksLikeClarifyingQuestion(message string) bool {
	message = strings.TrimSpace(message)
	if !strings.HasSuffix(message, "?") {
		return false
	}
	lower := strings.ToLower(message)
	return containsAny(lower, []string{"which ", "what ", "where ", "do you mean", "should i", "may i", "would you like", "can you clarify"})
}

func chatActionTarget(action ai.Action) string {
	switch {
	case action.NoteID > 0:
		return fmt.Sprintf("note:%d", action.NoteID)
	case strings.TrimSpace(action.Folder) != "":
		return action.Folder
	case strings.TrimSpace(action.Title) != "":
		return action.Title
	case len(action.Paths) > 0:
		return strings.Join(action.Paths, ", ")
	case len(action.Folders) > 0:
		return strings.Join(action.Folders, ", ")
	default:
		return ""
	}
}
