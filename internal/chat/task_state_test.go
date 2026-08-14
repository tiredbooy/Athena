package chat

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/retrieval"
	"github.com/tiredbooy/internal/storage"
	"github.com/tiredbooy/internal/tools"
)

type taskStateEmbedder struct{}

func (taskStateEmbedder) Name() string       { return "task-state-test" }
func (taskStateEmbedder) EmbedModel() string { return "task-state-test" }
func (taskStateEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

type clarificationFlowProvider struct{ calls int }

func (p *clarificationFlowProvider) Name() string        { return "ChatGPT subscription" }
func (p *clarificationFlowProvider) ChatModel() string   { return "test" }
func (p *clarificationFlowProvider) SetChatModel(string) {}
func (p *clarificationFlowProvider) ChatModels(context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (p *clarificationFlowProvider) ChatWithToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ai.ToolChatResult, error) {
	return ai.ToolChatResult{}, errors.New("mutation flow must require a structured decision")
}
func (p *clarificationFlowProvider) ChatWithRequiredToolsResult(_ context.Context, messages []models.Message, tools []models.ToolDefinition) (ai.ToolChatResult, error) {
	p.calls++
	if !proposalSchemaOffers(tools, "ensure_folders") || !proposalSchemaOffers(tools, "move_note") {
		return ai.ToolChatResult{}, errors.New("book organization actions were not offered")
	}
	switch p.calls {
	case 1:
		return decisionCall("request_clarification", `{"question":"What genre should I use for Project Mary Hill?"}`), nil
	case 2:
		if !messagesContain(messages, "Science Fiction") || !messagesContain(messages, "Andy Weir") || !messagesContain(messages, "ATHENA ACTIVE TASK STATE") {
			return ai.ToolChatResult{}, errors.New("first clarification answer lost its structured task context")
		}
		// This deliberately reproduces the old redundant permission question.
		// Athena should reject it internally because the pending-plan UI owns
		// confirmation, then request a corrected plan in the same user turn.
		return decisionCall("request_clarification", `{"question":"May I create Psychology and Science Fiction, then move the books?"}`), nil
	case 3:
		if !messagesContain(messages, "do not ask for a second prose confirmation") || !messagesContain(messages, "Project Mary Hill") {
			return ai.ToolChatResult{}, errors.New("redundant permission question was not corrected inside the active task")
		}
		return decisionCall("propose_actions", `{"summary":"Create the missing genre folders and organize the books","actions":[{"id":"folders","type":"ensure_folders","paths":["books/reading/Psychology","books/reading/Science Fiction"]},{"id":"psychology","type":"move_note","note_id":1,"folder":"books/reading/Psychology","depends_on":["folders"]},{"id":"science-fiction","type":"move_note","note_id":2,"folder":"books/reading/Science Fiction","depends_on":["folders"]},{"id":"metadata","type":"update_book_metadata","note_id":2,"authors":["Andy Weir"],"genres":["Science Fiction"]}]}`), nil
	default:
		return ai.ToolChatResult{}, errors.New("unexpected extra planning call")
	}
}
func (p *clarificationFlowProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}

func TestClarificationAnswerResumesGoalAndSkipsRedundantPermissionQuestion(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "athena.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vault := t.TempDir()
	retrievalService := retrieval.NewService(vault, storage.NewNoteStore(db), storage.NewChunkStore(db), taskStateEmbedder{})
	dispatcher := tools.NewDispatcher()
	for _, actionType := range []string{"ensure_folders", "move_note", "update_book_metadata"} {
		dispatcher.Register(actionType, func(context.Context, ai.Action) (string, error) { return "unused", nil })
	}
	provider := &clarificationFlowProvider{}
	session := NewSession(NewLoop(provider, map[string]ai.ChatProvider{"test": provider}, nil, retrievalService, dispatcher, nil))

	first := `yes exactly like the Computer Science folder; move all books into genre folders, and ask me about Project Mary Hill if its genre is unknown`
	reply, err := session.Submit(t.Context(), first, nil, nil)
	if err != nil || !strings.Contains(reply, "What genre") || session.pendingTask == nil {
		t.Fatalf("first reply=%q pending=%+v err=%v", reply, session.pendingTask, err)
	}
	reply, err = session.Submit(t.Context(), "Its genre is Science Fiction and the author is Andy Weir", nil, nil)
	if err != nil {
		t.Fatalf("resume after metadata answer: %v", err)
	}
	if session.pendingPlan == nil || len(session.pendingPlan.Actions) != 4 || !strings.Contains(reply, "Review required") {
		t.Fatalf("reply=%q plan=%+v", reply, session.pendingPlan)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls=%d, want clarification, rejected permission question, and corrected plan", provider.calls)
	}
}

func TestPendingTaskAttachesYesToItsQuestion(t *testing.T) {
	task := &PendingTask{OriginalGoal: "organize my books", Question: "Do you mean Project Hail Mary?", ExpectedAction: true}
	goal := task.resolvedGoal("yes")
	message := pendingTaskMessage(task, "yes")
	if !strings.Contains(goal, "organize my books") || !strings.Contains(goal, "User answered: yes") || !strings.Contains(message.Content, `"latest_answer":"yes"`) {
		t.Fatalf("goal=%q message=%q", goal, message.Content)
	}
}

func decisionCall(name, arguments string) ai.ToolChatResult {
	return ai.ToolChatResult{Message: models.Message{Role: "assistant", ToolCalls: []models.ToolCall{{
		ID: "call-" + name, Type: "function", Function: models.ToolCallFunction{Name: name, Arguments: json.RawMessage(arguments)},
	}}}}
}

func messagesContain(messages []models.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
}

func proposalSchemaOffers(tools []models.ToolDefinition, actionType string) bool {
	for _, tool := range tools {
		if tool.Function.Name != "propose_actions" {
			continue
		}
		properties := tool.Function.Parameters["properties"].(map[string]any)
		actions := properties["actions"].(map[string]any)
		items := actions["items"].(map[string]any)
		for _, raw := range items["oneOf"].([]any) {
			variant := raw.(map[string]any)
			typeSchema := variant["properties"].(map[string]any)["type"].(map[string]any)
			if typeSchema["enum"].([]string)[0] == actionType {
				return true
			}
		}
	}
	return false
}
