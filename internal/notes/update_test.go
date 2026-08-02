package notes

import "testing"

func TestReplaceMarkdownSectionPreservesOtherSections(t *testing.T) {
	body := "# Summary\nOld summary\n\n## Detail\nKeep this\n\n# Next\nUnchanged"
	updated, current, found := replaceMarkdownSection(body, "Summary", "New summary")
	if !found || current != "Old summary\n\n## Detail\nKeep this" {
		t.Fatalf("found=%t current=%q", found, current)
	}
	want := "# Summary\nNew summary\n# Next\nUnchanged"
	if updated != want {
		t.Fatalf("updated=%q, want %q", updated, want)
	}
}

func TestReplaceMarkdownSectionRejectsMissingSection(t *testing.T) {
	_, _, found := replaceMarkdownSection("# Present\nText", "Missing", "Replacement")
	if found {
		t.Fatal("found missing section")
	}
}
