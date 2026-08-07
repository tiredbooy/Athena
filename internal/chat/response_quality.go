package chat

import (
	"context"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

const (
	maxResponseRepairs        = 1
	maxResponseRepairDuration = 60 * time.Second
	minimumRepairBudget       = 20 * time.Second
)

// refineModelResponse gives a local model one focused correction pass when it
// claims it will change the vault but fails to produce executable actions, or
// when its action JSON fails the dispatcher contract. This belongs in the
// application layer: it coordinates the model and policy without teaching the
// domain layer about language-model phrasing.
func (s *Session) refineModelResponse(ctx context.Context, messages []models.Message, input, raw string, status func(string)) (string, error) {
	for attempt := 0; attempt < maxResponseRepairs; attempt++ {
		_, actions := ai.ExtractActions(raw)
		reason := ""
		if len(actions) > 0 && s.loop.dispatcher != nil {
			if err := s.loop.dispatcher.Validate(actions); err != nil {
				reason = "The proposed action plan failed validation: " + err.Error()
			}
		} else if len(actions) == 0 && expectsActionRequest(input) && claimsAction(raw) {
			reason = "The previous reply described a change but did not contain executable action JSON."
		}
		if reason == "" {
			return raw, nil
		}
		if !hasRepairBudget(ctx) {
			return raw, nil
		}

		if status != nil {
			status("The first plan was incomplete — refining it")
		}
		repairMessages := append([]models.Message(nil), messages...)
		repairMessages = append(repairMessages,
			models.Message{Role: "assistant", Content: raw},
			models.Message{Role: "user", Content: repairInstruction(reason)},
		)
		repairCtx, cancel := context.WithTimeout(ctx, maxResponseRepairDuration)
		refined, err := s.runReadToolLoop(repairCtx, repairMessages, status)
		cancel()
		if err != nil {
			// A correction pass is an optional quality improvement. Preserve the
			// original reply so the caller can safely decline an unexecutable plan
			// instead of reporting a second model timeout as an application error.
			return raw, nil
		}
		raw = refined
	}
	return raw, nil
}

func hasRepairBudget(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) >= minimumRepairBudget
}

func repairInstruction(reason string) string {
	return "Correction required. " + reason + " Re-read the authoritative vault context and use read-only tools if an exact note_id or folder is missing. Then return one concise user-facing answer followed by valid fenced action JSON for every requested change. Do not claim that anything changed until Athena executes it. A folder field is a directory only; never put a note title or .md filename in it. If the request is ambiguous, ask one precise question instead of guessing."
}

func expectsActionRequest(input string) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" || strings.Contains(input, "how do i") || strings.Contains(input, "how can i") || strings.Contains(input, "how to ") || strings.Contains(input, "can you explain") || strings.Contains(input, "could you explain") || strings.HasPrefix(input, "why ") || strings.HasPrefix(input, "what is ") || strings.HasPrefix(input, "explain ") {
		return false
	}
	return containsAny(input, []string{
		"create", "add", "make", "write", "save", "move", "rename", "delete", "remove", "update", "edit", "append", "archive", "restore", "link", "connect", "disconnect", "unlink", "mark", "finish",
	}) && containsAny(input, []string{"note", "folder", "directory", "file", "vault", "task", "book", "journal", "idea"})
}

func claimsAction(reply string) bool {
	reply = strings.ToLower(strings.TrimSpace(reply))
	return containsAny(reply, []string{
		"i'll", "i will", "i can", "sure", "creating", "creating a", "moving", "renaming", "deleting", "updating", "saving", "archiving", "restoring", "created", "moved", "renamed", "deleted", "updated", "saved", "done",
	})
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
