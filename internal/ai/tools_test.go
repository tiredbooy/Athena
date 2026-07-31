package ai

import (
	"strings"
	"testing"
)

func TestExtractActions_ClosedFence(t *testing.T) {
	raw := "Sure, done.\n\n```action\n" +
		`{"type": "create_note", "title": "Reading DDIA", "content": "Finished chapter 1.", "tags": ["books"]}` +
		"\n```\n"

	cleaned, found := ExtractActions(raw)
	if len(found) != 1 {
		t.Fatalf("expected 1 action, got %d", len(found))
	}
	if found[0].Type != "create_note" || found[0].Title != "Reading DDIA" {
		t.Fatalf("unexpected action: %+v", found[0])
	}
	if strings.Contains(cleaned, "```") || strings.Contains(cleaned, "create_note") {
		t.Fatalf("cleaned text still has fence/action: %q", cleaned)
	}
	if !strings.Contains(cleaned, "Sure, done.") {
		t.Fatalf("cleaned text lost prose: %q", cleaned)
	}
}

func TestExtractActions_UnclosedFence(t *testing.T) {
	// qwen and other small models often forget the closing ```.
	raw := "```action\n" +
		`{"type": "create_note", "title": "Reading: Designing Data-Intensive Applications", "content": "[Book Title]: \"Designing Data-IntensiveApplications\" by Martin Kleppmann\n[Year Published] 2017/2018.", "tags": ["technology", "data-engineering", "books"]}`

	cleaned, found := ExtractActions(raw)
	if len(found) != 1 {
		t.Fatalf("expected 1 action from unclosed fence, got %d (cleaned=%q)", len(found), cleaned)
	}
	if found[0].Title != "Reading: Designing Data-Intensive Applications" {
		t.Fatalf("title = %q", found[0].Title)
	}
	if cleaned != "" {
		t.Fatalf("expected empty cleaned text, got %q", cleaned)
	}
}

func TestExtractActions_BraceInsideString(t *testing.T) {
	raw := "```action\n" +
		`{"type": "create_note", "title": "Braces", "content": "use {curly} braces carefully", "tags": []}` +
		"\n```"

	_, found := ExtractActions(raw)
	if len(found) != 1 {
		t.Fatalf("expected 1 action, got %d", len(found))
	}
	if found[0].Content != "use {curly} braces carefully" {
		t.Fatalf("content truncated: %q", found[0].Content)
	}
}

func TestExtractActions_Multiple(t *testing.T) {
	raw := "Creating both.\n" +
		"```action\n" + `{"type": "create_note", "title": "A", "content": "a"}` + "\n```\n" +
		"```action\n" + `{"type": "create_task", "title": "B", "content": "b"}` + "\n```\n"

	cleaned, found := ExtractActions(raw)
	if len(found) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(found))
	}
	if found[0].Type != "create_note" || found[1].Type != "create_task" {
		t.Fatalf("types = %s, %s", found[0].Type, found[1].Type)
	}
	if strings.Contains(cleaned, "```") {
		t.Fatalf("cleaned still has fences: %q", cleaned)
	}
}

func TestExtractActions_BatchArray(t *testing.T) {
	raw := "```action\n[" +
		`{"id":"one","type":"create_folder","folder":"projects/a"},` +
		`{"id":"two","type":"create_folder","folder":"projects/b"}` +
		"]\n```"

	cleaned, found := ExtractActions(raw)
	if len(found) != 2 || found[0].ID != "one" || found[1].ID != "two" {
		t.Fatalf("actions = %+v, want two batch actions", found)
	}
	if cleaned != "" {
		t.Fatalf("cleaned = %q, want empty", cleaned)
	}
}

func TestExtractActions_BatchEnvelope(t *testing.T) {
	raw := "```action\n" +
		`{"actions":[{"id":"one","type":"create_folder","folder":"projects/a"}]}` +
		"\n```"

	_, found := ExtractActions(raw)
	if len(found) != 1 || found[0].ID != "one" {
		t.Fatalf("actions = %+v, want envelope action", found)
	}
}

func TestExtractActions_NormalizesWeakModelFolderAction(t *testing.T) {
	raw := "```action\n" + `{"type":"createfolder","path":"projects/ideas"}` + "\n```"
	_, found := ExtractActions(raw)
	if len(found) != 1 || found[0].Type != "create_folder" || found[0].Folder != "projects/ideas" {
		t.Fatalf("actions = %+v, want normalized create folder action", found)
	}
}

func TestExtractActions_NoFence(t *testing.T) {
	raw := "Just a normal reply with no tools."
	cleaned, found := ExtractActions(raw)
	if len(found) != 0 {
		t.Fatalf("expected 0 actions, got %d", len(found))
	}
	if cleaned != raw {
		t.Fatalf("cleaned changed: %q", cleaned)
	}
}

func TestExtractActions_MalformedJSONSkipped(t *testing.T) {
	raw := "Oops.\n```action\n{not json}\n```\n"
	cleaned, found := ExtractActions(raw)
	if len(found) != 0 {
		t.Fatalf("expected malformed to be skipped, got %+v", found)
	}
	if !strings.Contains(cleaned, "Oops.") {
		t.Fatalf("lost prose: %q", cleaned)
	}
}
