package tui

import (
	"regexp"
	"strings"
)

var (
	markdownLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	bold         = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	italic       = regexp.MustCompile(`\*([^*]+)\*|_([^_]+)_`)
)

// RenderMarkdown turns the small, common Markdown subset emitted by local
// models into quiet terminal text. It deliberately keeps content plain: a
// note title should not become a code block just because a model chose one.
func RenderMarkdown(text string) string {
	var out []string
	for _, raw := range strings.Split(strings.TrimSpace(text), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			continue
		}
		line = strings.TrimLeft(line, "#")
		line = strings.TrimSpace(line)
		line = markdownLink.ReplaceAllString(line, "$1 ($2)")
		line = bold.ReplaceAllString(line, "$1$2")
		line = italic.ReplaceAllString(line, "$1$2")
		line = strings.ReplaceAll(line, "\\_", "_")
		line = strings.ReplaceAll(line, "\\*", "*")
		line = strings.ReplaceAll(line, "`", "")
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			line = "• " + strings.TrimSpace(line[2:])
		}
		if strings.HasPrefix(line, "> ") {
			line = "│ " + strings.TrimSpace(line[2:])
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
