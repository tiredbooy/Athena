package chat

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/tiredbooy/internal/models"
)

// PendingTask is the conversation-level state for a goal that Athena paused
// to ask the user a question. It is deliberately separate from PendingPlan:
// questions collect missing intent, while plans contain validated actions that
// are ready for review.
type PendingTask struct {
	OriginalGoal   string    `json:"original_goal"`
	Question       string    `json:"pending_question"`
	Answers        []string  `json:"answers,omitempty"`
	ExpectedAction bool      `json:"expected_action"`
	CreatedAt      time.Time `json:"created_at"`
}

func (task *PendingTask) resolvedGoal(answer string) string {
	if task == nil {
		return strings.TrimSpace(answer)
	}
	var out strings.Builder
	out.WriteString(strings.TrimSpace(task.OriginalGoal))
	for _, prior := range task.Answers {
		if strings.TrimSpace(prior) != "" {
			out.WriteString("\nClarification answer: ")
			out.WriteString(strings.TrimSpace(prior))
		}
	}
	out.WriteString("\nAthena asked: ")
	out.WriteString(strings.TrimSpace(task.Question))
	out.WriteString("\nUser answered: ")
	out.WriteString(strings.TrimSpace(answer))
	return strings.TrimSpace(out.String())
}

func pendingTaskMessage(task *PendingTask, answer string) models.Message {
	state := struct {
		OriginalGoal    string   `json:"original_goal"`
		PendingQuestion string   `json:"pending_question"`
		PriorAnswers    []string `json:"prior_answers,omitempty"`
		LatestAnswer    string   `json:"latest_answer"`
		ExpectedAction  bool     `json:"expected_action"`
	}{
		OriginalGoal: task.OriginalGoal, PendingQuestion: task.Question,
		PriorAnswers: append([]string(nil), task.Answers...), LatestAnswer: strings.TrimSpace(answer),
		ExpectedAction: task.ExpectedAction,
	}
	raw, _ := json.Marshal(state)
	return models.Message{Role: "system", Content: "[ATHENA ACTIVE TASK STATE — APPLICATION DATA]\n" + string(raw) + "\n[END ATHENA ACTIVE TASK STATE]\nInterpret the latest answer in relation to the pending question and original goal. Continue the task, ask a new precise question if information is still missing, or finish if the user changed or cancelled the goal."}
}
