package retrieval

import "testing"

func TestWikiTargetsAcceptsAliasesAndPaths(t *testing.T) {
	targets := wikiTargets("See [[books/foundation|Foundation]] and [[Plan]].")
	if !targets["foundation"] || !targets["plan"] {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestNormalizeTitleForDuplicates(t *testing.T) {
	if normalize("Go: Notes!") != normalize("go notes") {
		t.Fatal("equivalent titles did not normalize together")
	}
}
