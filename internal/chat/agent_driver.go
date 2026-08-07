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
- Propose the smallest necessary action plan. Prefer the propose_actions tool when available; otherwise use fenced action JSON.
- Finish only when the goal is answered or verified execution observations prove every requested change succeeded.
- Ask one specific question only when the missing information cannot be resolved from inventory or read tools.
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
	result, err := d.session.runReadToolLoopState(ctx, state.Messages, status)
	if err != nil {
		return agent.Decision{}, err
	}
	state.Messages = result.Messages
	cleaned, actions := ai.ExtractActions(result.Content)
	if len(actions) > 0 {
		actions = d.session.refineNoteTitles(ctx, state.Goal, actions, status)
		return agent.Decision{Kind: agent.DecisionAct, Message: cleaned, Raw: result.Content, Actions: actions}, nil
	}
	kind := agent.DecisionFinish
	if looksLikeClarifyingQuestion(cleaned) {
		kind = agent.DecisionAskUser
	}
	return agent.Decision{Kind: kind, Message: cleaned, Raw: result.Content}, nil
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
			return fmt.Errorf("the proposed action plan failed validation: %w", err)
		}
		if warning := implicitFolderCreationWarning(state.Goal, decision.Actions); warning != "" {
			return fmt.Errorf("%s Inspect the existing folder tree and choose a plan that stays within the user's request", warning)
		}
		return nil
	}
	if state.ContextSupplied && asksUserForVaultInventory(decision.Message) {
		return fmt.Errorf("the complete vault inventory was already supplied; use it instead of asking the user to provide it")
	}
	if state.ExpectedAction && len(state.Completed) == 0 && claimsAction(decision.Raw) {
		return fmt.Errorf("the reply claimed a vault change but did not provide executable action data")
	}
	return nil
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
	return containsAny(lower, []string{"which ", "what ", "where ", "do you mean", "should i", "would you like", "can you clarify"})
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
