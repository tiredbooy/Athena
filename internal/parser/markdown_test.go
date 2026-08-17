package parser

import (
	"strings"
	"testing"
	"time"
)

// A note synced from Windows or Obsidian arrives with CRLF line endings.
// It must yield the same frontmatter and body as the LF-authored original,
// otherwise the whole file silently falls through as untitled, untagged body
// text and the note loses its metadata in the vault.
func TestParseMarkdownCRLFMatchesLF(t *testing.T) {
	lf := "---\ntitle: Go nil slices\ntags: [go, backend]\nkind: note\n---\n\nA nil slice has no backing array.\n\nAppending to it allocates.\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")

	lfFrontmatter, lfBody, err := ParseMarkdown(lf)
	if err != nil {
		t.Fatalf("parse LF note: %v", err)
	}
	crlfFrontmatter, crlfBody, err := ParseMarkdown(crlf)
	if err != nil {
		t.Fatalf("parse CRLF note: %v", err)
	}

	if crlfFrontmatter.Title != lfFrontmatter.Title {
		t.Errorf("title: CRLF got %q, LF got %q", crlfFrontmatter.Title, lfFrontmatter.Title)
	}
	if strings.Join(crlfFrontmatter.Tags, ",") != strings.Join(lfFrontmatter.Tags, ",") {
		t.Errorf("tags: CRLF got %v, LF got %v", crlfFrontmatter.Tags, lfFrontmatter.Tags)
	}
	if crlfFrontmatter.Kind != lfFrontmatter.Kind {
		t.Errorf("kind: CRLF got %q, LF got %q", crlfFrontmatter.Kind, lfFrontmatter.Kind)
	}
	if crlfBody != lfBody {
		t.Errorf("body: CRLF got %q, LF got %q", crlfBody, lfBody)
	}
}

// A CRLF note without frontmatter still has to come back as usable body text.
func TestParseMarkdownCRLFWithoutFrontmatter(t *testing.T) {
	frontmatter, body, err := ParseMarkdown("# Heading\r\n\r\nSome prose.\r\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if frontmatter.Title != "" {
		t.Errorf("expected no title, got %q", frontmatter.Title)
	}
	if !strings.Contains(body, "# Heading") || !strings.Contains(body, "Some prose.") {
		t.Errorf("body lost content: %q", body)
	}
}

// Notes are read, edited and written back repeatedly (folder links, book
// metadata, section updates). Every field the vault stores must survive a
// render/parse cycle or an edit to one field wipes the others.
func TestRenderParseRoundTrip(t *testing.T) {
	started := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 4, 12, 18, 30, 0, 0, time.UTC)
	original := Frontmatter{
		Title:          "Dune",
		Tags:           []string{"scifi", "reading"},
		AthenaIndex:    true,
		LinkedFolders:  []string{"books", "notes/scifi"},
		Kind:           "book",
		Authors:        []string{"Frank Herbert"},
		Genres:         []string{"science fiction"},
		PublishedYear:  1965,
		ISBN:           "9780441013593",
		MetadataSource: "openlibrary",
		StartedAt:      started,
		FinishedAt:     &finished,
	}
	body := "## Notes\n\nSpice must flow.\n\n## Quotes\n\n> Fear is the mind-killer.\n"

	rendered, err := RenderMarkdown(original, body)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	parsed, parsedBody, err := ParseMarkdown(rendered)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if parsedBody != body {
		t.Errorf("body: got %q, want %q", parsedBody, body)
	}
	if parsed.Title != original.Title {
		t.Errorf("title: got %q, want %q", parsed.Title, original.Title)
	}
	if strings.Join(parsed.Tags, ",") != strings.Join(original.Tags, ",") {
		t.Errorf("tags: got %v, want %v", parsed.Tags, original.Tags)
	}
	if !parsed.AthenaIndex {
		t.Error("athena_index was lost")
	}
	if strings.Join(parsed.LinkedFolders, ",") != strings.Join(original.LinkedFolders, ",") {
		t.Errorf("linked_folders: got %v, want %v", parsed.LinkedFolders, original.LinkedFolders)
	}
	if parsed.Kind != original.Kind {
		t.Errorf("kind: got %q, want %q", parsed.Kind, original.Kind)
	}
	if strings.Join(parsed.Authors, ",") != strings.Join(original.Authors, ",") {
		t.Errorf("authors: got %v, want %v", parsed.Authors, original.Authors)
	}
	if strings.Join(parsed.Genres, ",") != strings.Join(original.Genres, ",") {
		t.Errorf("genres: got %v, want %v", parsed.Genres, original.Genres)
	}
	if parsed.PublishedYear != original.PublishedYear {
		t.Errorf("published_year: got %d, want %d", parsed.PublishedYear, original.PublishedYear)
	}
	if parsed.ISBN != original.ISBN {
		t.Errorf("isbn: got %q, want %q", parsed.ISBN, original.ISBN)
	}
	if parsed.MetadataSource != original.MetadataSource {
		t.Errorf("metadata_source: got %q, want %q", parsed.MetadataSource, original.MetadataSource)
	}
	if !parsed.StartedAt.Equal(started) {
		t.Errorf("started_at: got %v, want %v", parsed.StartedAt, started)
	}
	if parsed.FinishedAt == nil || !parsed.FinishedAt.Equal(finished) {
		t.Errorf("finished_at: got %v, want %v", parsed.FinishedAt, finished)
	}
}

// A ticked task's `done:` has to survive the read/write cycle every note goes
// through, or the flag is lost by the next body edit. The un-ticked case must
// round trip as an *absent* key: notes, books and folder index notes all share
// this struct, and a `done: false` appearing in each of them on first write
// would churn the whole vault for a field only tasks use.
func TestRenderParseRoundTripDone(t *testing.T) {
	for _, done := range []bool{true, false} {
		rendered, err := RenderMarkdown(Frontmatter{Title: "Ship V-06", Kind: "task", Done: done}, "body\n")
		if err != nil {
			t.Fatalf("render done=%t: %v", done, err)
		}
		if strings.Contains(rendered, "done:") != done {
			t.Errorf("done=%t rendered as %q", done, rendered)
		}
		parsed, body, err := ParseMarkdown(rendered)
		if err != nil {
			t.Fatalf("parse done=%t: %v", done, err)
		}
		if parsed.Done != done {
			t.Errorf("done: got %t, want %t", parsed.Done, done)
		}
		if body != "body\n" {
			t.Errorf("body: got %q, want %q", body, "body\n")
		}
	}
}

// A note whose body is empty must still round trip: the title is the only
// thing that makes it findable, so losing the frontmatter loses the note.
func TestRenderParseRoundTripEmptyBody(t *testing.T) {
	rendered, err := RenderMarkdown(Frontmatter{Title: "Empty note", Tags: []string{"inbox"}}, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	parsed, body, err := ParseMarkdown(rendered)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body != "" {
		t.Errorf("body: got %q, want empty", body)
	}
	if parsed.Title != "Empty note" {
		t.Errorf("title: got %q, want %q", parsed.Title, "Empty note")
	}
	if strings.Join(parsed.Tags, ",") != "inbox" {
		t.Errorf("tags: got %v, want [inbox]", parsed.Tags)
	}
}

// Vaults contain plenty of files Athena did not write. None of them may
// panic or lose their content.
func TestParseMarkdownWithoutUsableFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty file", ""},
		{"plain markdown", "# Title\n\nJust prose, no frontmatter.\n"},
		{"opening delimiter only", "---\ntitle: never closed\n"},
		{"bare delimiter", "---\n"},
		{"delimiter not at start", "\n---\ntitle: too late\n---\nbody\n"},
		{"horizontal rule first", "---\n\nA thematic break, not frontmatter.\n"},
		{"dashes mid word", "well---formed? no.\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			frontmatter, body, err := ParseMarkdown(testCase.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if frontmatter.Title != "" {
				t.Errorf("expected no title, got %q", frontmatter.Title)
			}
			for _, word := range strings.Fields(testCase.raw) {
				if !strings.Contains(body, word) {
					t.Fatalf("body %q dropped %q from input %q", body, word, testCase.raw)
				}
			}
		})
	}
}

// Broken YAML must surface as an error the caller can report, not a panic
// and not a silently empty note.
func TestParseMarkdownInvalidYAMLReturnsError(t *testing.T) {
	_, _, err := ParseMarkdown("---\ntitle: [unterminated\ntags: {oops\n---\nbody\n")
	if err == nil {
		t.Fatal("expected an error for malformed YAML, got nil")
	}
}

// The frontmatter block is YAML, so a title with a colon or leading dashes
// has to come back verbatim rather than truncated at the punctuation.
func TestRenderParseRoundTripAwkwardTitle(t *testing.T) {
	const title = "Go: why --- matters, \"really\""
	rendered, err := RenderMarkdown(Frontmatter{Title: title}, "body\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	parsed, body, err := ParseMarkdown(rendered)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Title != title {
		t.Errorf("title: got %q, want %q", parsed.Title, title)
	}
	if body != "body\n" {
		t.Errorf("body: got %q, want %q", body, "body\n")
	}
}
