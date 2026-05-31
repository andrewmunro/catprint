// Pre-render markdown validator. Catches LLM mistakes before they hit the
// paper — line too long, heading too long, unsupported block types. Returns
// structured errors so the caller (MCP) can surface line-specific feedback
// for the LLM to self-correct.
package validate

import (
	"strings"
	"unicode/utf8"

	"github.com/synestry/catprint/printer"
)

// Violation describes a single problem in the input.
type Violation struct {
	Line    int    `json:"line"`
	Issue   string `json:"issue"`
	Actual  int    `json:"actual,omitempty"`
	Content string `json:"content"`
}

// Result is what Validate returns. Empty Violations means OK.
type Result struct {
	Error      string      `json:"error,omitempty"`
	Violations []Violation `json:"violations,omitempty"`
}

// OK reports whether the result has no violations.
func (r Result) OK() bool { return len(r.Violations) == 0 }

// Validate scans markdown line-by-line against the printer's constraints.
// Empty content is treated as valid (LLM may submit a blank line — renderer
// handles it gracefully).
func Validate(md string) Result {
	var vs []Violation
	lines := strings.Split(md, "\n")

	const (
		fenceMarker = "```"
		tablePipe   = "|"
	)
	inFence := false

	for i, raw := range lines {
		lineNum := i + 1
		trimmed := strings.TrimRight(raw, "\r")
		stripped := strings.TrimSpace(trimmed)

		// Track unsupported code blocks but also reject them.
		if strings.HasPrefix(stripped, fenceMarker) {
			vs = append(vs, Violation{
				Line: lineNum, Issue: "code blocks are not supported",
				Content: trimmed,
			})
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		// Unsupported syntax flags.
		switch {
		case strings.Contains(trimmed, tablePipe) && looksLikeTable(trimmed):
			vs = append(vs, Violation{
				Line: lineNum, Issue: "tables are not supported", Content: trimmed,
			})
		case strings.Contains(trimmed, "!["):
			vs = append(vs, Violation{
				Line: lineNum, Issue: "inline images are not supported", Content: trimmed,
			})
		case hasMarkdownLink(trimmed):
			vs = append(vs, Violation{
				Line: lineNum, Issue: "links are not supported", Content: trimmed,
			})
		}

		// Heading length check.
		if h, body := splitHeading(stripped); h > 0 {
			if utf8.RuneCountInString(body) > printer.HeadingMaxChars {
				vs = append(vs, Violation{
					Line:    lineNum,
					Issue:   "heading exceeds heading_max_chars",
					Actual:  utf8.RuneCountInString(body),
					Content: trimmed,
				})
			}
			continue
		}

		// General line length check (after stripping bullet/checkbox markers).
		body := stripListMarker(trimmed)
		if n := utf8.RuneCountInString(body); n > printer.MaxLineLengthChars {
			vs = append(vs, Violation{
				Line: lineNum, Issue: "exceeds max_line_length_chars",
				Actual: n, Content: trimmed,
			})
		}
	}

	r := Result{Violations: vs}
	if !r.OK() {
		r.Error = "validation_failed"
	}
	return r
}

// splitHeading returns (level, body) for "# Title" lines or (0, "") otherwise.
func splitHeading(s string) (int, string) {
	level := 0
	for level < len(s) && s[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, ""
	}
	if level == len(s) || s[level] != ' ' {
		return 0, ""
	}
	return level, strings.TrimSpace(s[level:])
}

// stripListMarker removes bullet, checkbox, or numbered prefix for length checks.
// "- [ ] foo" → "foo", "- foo" → "foo", "1. foo" → "foo".
func stripListMarker(s string) string {
	t := strings.TrimLeft(s, " \t")
	switch {
	case strings.HasPrefix(t, "- [ ] "), strings.HasPrefix(t, "- [x] "), strings.HasPrefix(t, "- [X] "):
		return t[6:]
	case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "* "), strings.HasPrefix(t, "+ "):
		return t[2:]
	}
	// Numbered list: "1. " up to "999. ".
	for i := 0; i < len(t) && i < 4; i++ {
		if t[i] < '0' || t[i] > '9' {
			if i > 0 && i+1 < len(t) && t[i] == '.' && t[i+1] == ' ' {
				return t[i+2:]
			}
			break
		}
	}
	return t
}

// looksLikeTable matches "| a | b |" rows or "|---|---|" separators.
func looksLikeTable(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "|") {
		return false
	}
	// At least one more pipe after stripping the leading one.
	return strings.Count(t, "|") >= 2
}

// hasMarkdownLink detects [text](url) — not perfect but catches typical LLM output.
func hasMarkdownLink(s string) bool {
	for i := 0; i < len(s)-3; i++ {
		if s[i] != '[' {
			continue
		}
		closeBracket := strings.Index(s[i:], "]")
		if closeBracket < 0 {
			return false
		}
		j := i + closeBracket
		if j+1 < len(s) && s[j+1] == '(' && strings.Contains(s[j:], ")") {
			return true
		}
	}
	return false
}
