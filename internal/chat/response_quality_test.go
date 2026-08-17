package chat

import "testing"

func TestExpectsActionRequestDistinguishesQuestions(t *testing.T) {
	if !expectsActionRequest("create a note in work") {
		t.Fatal("create request was not detected")
	}
	if !expectsActionRequest("can you move this note to archive") {
		t.Fatal("polite change request was not detected")
	}
	if !expectsActionRequest(`organize my "books" into genre folders and style them`) {
		t.Fatal("book organization request was not detected")
	}
	if expectsActionRequest("how do I create a note") {
		t.Fatal("how-to question was treated as a vault change")
	}
}

func TestClaimsActionDetectsPromisesButNotFactualQuestions(t *testing.T) {
	if !claimsAction("I'll create that note now.") {
		t.Fatal("action promise was not detected")
	}
	if claimsAction("What is this note about?") {
		t.Fatal("question was treated as an action promise")
	}
}

// R-03: the runner spends one correction and then stops safely. A model that
// says it cannot act, or needs a detail first, has not claimed a vault change —
// rejecting it would spend that single correction on a correct reply and end
// the turn with a safe stop instead of the model's question.
func TestClaimsActionIgnoresInabilityAndRequestsForDetail(t *testing.T) {
	for _, reply := range []string{
		"I can't create that note without knowing which folder you mean.",
		"I cannot move a note until you tell me its ID.",
		"I'll need the exact folder name before I rename anything.",
		"Please make sure the destination folder exists first.",
	} {
		if claimsAction(reply) {
			t.Fatalf("a request for detail was treated as an unbacked action claim: %q", reply)
		}
	}
	if !claimsAction("Sure, that note is in your work folder now.") {
		t.Fatal("a leading agreement was no longer detected as a claim")
	}
	if !claimsAction("I can create that note for you.") {
		t.Fatal("an unbacked capability promise was no longer detected as a claim")
	}
}

// R-04: every shortcut phrasing must be a complete, read-only listing request.
// A compound sentence must reach the model, because the shortcut answers only
// the listing half and would silently discard the rest.
func TestListingShortcutMatchesOnlyCompleteReadOnlyRequests(t *testing.T) {
	for _, input := range []string{
		"list notes", "List all my notes.", "  show   me  all my notes  ", "What notes are in my vault?", "all my notes",
	} {
		if !isListingRequest(input) {
			t.Fatalf("exact listing request was not recognised: %q", input)
		}
	}
	for _, input := range []string{
		"list my notes and delete the old ones",
		"show my notes from last week",
		"list my notes in work",
		"what's in my vault",
		"delete my notes",
	} {
		if isListingRequest(input) {
			t.Fatalf("shortcut fired on a request it cannot fully answer: %q", input)
		}
	}
}
