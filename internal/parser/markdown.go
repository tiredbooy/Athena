package parser

import (
	"strings"

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
type Frontmatter struct {
	Title string   `yaml:"title"`
	Tags  []string `yaml:"tags"`
}

// ParseMarkdown splits raw file content into its Frontmatter and body.
// If there's no frontmatter block, it returns a zero-value Frontmatter
// and the entire input as the body — so this is safe to call on plain
// markdown files too, not just ones we generated ourselves.
func ParseMarkdown(raw string) (Frontmatter, string, error) {
	var fm Frontmatter

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
