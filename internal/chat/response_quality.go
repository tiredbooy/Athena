package chat

import "strings"

// These classifiers decide, from the user's own words and the model's reply,
// whether a turn was supposed to change the vault and whether the model only
// claimed it did. The agent driver (R-03) owns what to do about that; this file
// only answers the question.

func expectsActionRequest(input string) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" || strings.Contains(input, "how do i") || strings.Contains(input, "how can i") || strings.Contains(input, "how to ") || strings.Contains(input, "can you explain") || strings.Contains(input, "could you explain") || strings.HasPrefix(input, "why ") || strings.HasPrefix(input, "what is ") || strings.HasPrefix(input, "explain ") {
		return false
	}
	return containsAny(input, []string{
		"create", "add", "make", "write", "save", "move", "rename", "delete", "remove", "update", "edit", "append", "archive", "restore", "link", "connect", "disconnect", "unlink", "mark", "finish", "organize", "organise", "style", "color", "colour",
	}) && containsAny(input, []string{"note", "folder", "directory", "file", "vault", "task", "book", "journal", "idea"})
}

// notClaiming strips the phrasings in which a model states it cannot act, or
// needs more information, before the weak claim tokens below are matched. Both
// forms are the opposite of a claim — the model is asking, not pretending the
// vault changed — but "i can't" contains "i can" and "i'll need" contains
// "i'll". R-03 spends one correction and then stops, so a false positive here
// burns that single correction on a reply that was already correct.
var notClaiming = strings.NewReplacer(
	"i can't", "", "i cannot", "", "i can not", "",
	"i'll need", "", "i will need", "", "i'd need", "", "i would need", "",
)

func claimsAction(reply string) bool {
	reply = strings.ToLower(strings.TrimSpace(reply))
	// "Sure" reads as agreement only at the very start of the reply. Mid-sentence
	// it is almost always "make sure" or "ensure", which is advice.
	if strings.HasPrefix(reply, "sure") {
		return true
	}
	return containsAny(notClaiming.Replace(reply), []string{
		"i'll", "i will", "i can", "creating", "moving", "renaming", "deleting", "updating", "saving", "archiving", "restoring", "created", "moved", "renamed", "deleted", "updated", "saved", "done",
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
