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
