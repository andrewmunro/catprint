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
func Validate(md string) Result { return validate(md, true) }

// ValidateShared is the relaxed variant for human-shared plain text (Web Share
// Target). It skips the per-line length check: shared text routinely exceeds
// the 32-char paper width, and the renderer word-wraps long lines anyway. The
// runaway-job and unsupported-block checks still apply.
func ValidateShared(md string) Result { return validate(md, false) }

func validate(md string, checkLineLength bool) Result {
	var vs []Violation
	lines := strings.Split(md, "\n")

	// Reject runaway jobs before rendering — a single print shouldn't eat the
	// roll. Count non-blank content lines so trailing newlines don't trip it.
	if n := countContentLines(lines); n > printer.MaxContentLines {
		vs = append(vs, Violation{
			Line:    0,
			Issue:   "exceeds max_content_lines",
			Actual:  n,
			Content: "too many lines for one print; split into multiple prints",
		})
	}

	const (
		fenceMarker = "```"
		tablePipe   = "|"
	)
	inFence := false

	for i, raw := range lines {
		lineNum := i + 1
		trimmed := strings.TrimRight(raw, "\r")
		stripped := strings.TrimSpace(trimmed)

		// Code fences delimit verbatim monospace blocks (ASCII art, code).
		// Content inside is exempt from the line-length and heading checks —
		// it's rendered as-is in a monospace face, auto-scaled to fit width.
		if strings.HasPrefix(stripped, fenceMarker) {
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
		if checkLineLength {
			body := stripListMarker(trimmed)
			if n := utf8.RuneCountInString(body); n > printer.MaxLineLengthChars {
				vs = append(vs, Violation{
					Line: lineNum, Issue: "exceeds max_line_length_chars",
					Actual: n, Content: trimmed,
				})
			}
		}
	}

	r := Result{Violations: vs}
	if !r.OK() {
		r.Error = "validation_failed"
	}
	return r
}

// countContentLines counts lines with any non-whitespace content.
func countContentLines(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
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
