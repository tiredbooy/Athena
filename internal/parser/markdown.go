package parser

import (
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the YAML metadata block at the top of a note, delimited
// by --- lines, e.g.:
//
//	---
//	title: Go nil slices
//	tags: [go, backend]
//	---
//	body content here...
//
// Done carries a task's completion state (V-06). `omitempty` is deliberate:
// "absent" and "false" both mean not done, so an untouched note is not rewritten
// with a `done: false` line the first time Athena writes it, and un-ticking a
// task removes the key again rather than leaving it behind.
type Frontmatter struct {
	Title          string     `yaml:"title"`
	Tags           []string   `yaml:"tags"`
	AthenaIndex    bool       `yaml:"athena_index,omitempty"`
	LinkedFolders  []string   `yaml:"linked_folders,omitempty"`
	Kind           string     `yaml:"kind,omitempty"`
	Done           bool       `yaml:"done,omitempty"`
	Authors        []string   `yaml:"authors,omitempty"`
	Genres         []string   `yaml:"genres,omitempty"`
	PublishedYear  int        `yaml:"published_year,omitempty"`
	ISBN           string     `yaml:"isbn,omitempty"`
	MetadataSource string     `yaml:"metadata_source,omitempty"`
	StartedAt      time.Time  `yaml:"started_at,omitempty"`
	FinishedAt     *time.Time `yaml:"finished_at,omitempty"`
}

// ParseMarkdown splits raw file content into its Frontmatter and body.
// If there's no frontmatter block, it returns a zero-value Frontmatter
// and the entire input as the body — so this is safe to call on plain
// markdown files too, not just ones we generated ourselves.
func ParseMarkdown(raw string) (Frontmatter, string, error) {
	var fm Frontmatter

	// Notes authored on Windows or synced through Obsidian use CRLF. Without
	// this the "---\n" delimiter never matches and the whole file — metadata
	// included — falls through as plain body text. Every consumer of the body
	// (rendering, section splitting, chunking) works in LF anyway.
	raw = strings.ReplaceAll(raw, "\r\n", "\n")

	if !strings.HasPrefix(raw, "---\n") {
		return fm, raw, nil
	}

	rest := raw[4:] // skip the opening "---\n"
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return fm, raw, nil // malformed/no closing delimiter, treat as plain body
	}

	yamlBlock := rest[:end]
	body := strings.TrimLeft(rest[end+4:], "\n")

	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return fm, raw, err
	}
	return fm, body, nil
}

// RenderMarkdown does the reverse: builds a full file's content from
// frontmatter + body, for when we write a note back to disk.
func RenderMarkdown(fm Frontmatter, body string) (string, error) {
	yamlBlock, err := yaml.Marshal(fm)
	if err != nil {
		return "", err
	}
	return "---\n" + string(yamlBlock) + "---\n\n" + body, nil
}
