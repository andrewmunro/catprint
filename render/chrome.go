// Auto-injected chrome: timestamp header + tearline footer. LLMs never
// generate these — the server owns paper chrome end to end.
package render

import (
	"fmt"
	"image"
	"strings"
	"time"
)

// Chrome captures the data needed for header/footer in a single struct so
// callers don't accidentally render with default zero values.
type Chrome struct {
	Now    time.Time
	Source string // "mcp" | "web" | "apk"
}

// DefaultChrome returns chrome with current time and the given source tag.
func DefaultChrome(source string) Chrome {
	return Chrome{Now: time.Now(), Source: source}
}

// formatHeader returns the timestamp line, e.g. "Sat 31 May · 07:32  [web]".
func formatHeader(c Chrome) string {
	src := c.Source
	if src == "" {
		src = "?"
	}
	return fmt.Sprintf("%s · %s  [%s]",
		c.Now.Format("Mon 2 Jan"), c.Now.Format("15:04"), src)
}

// formatTearline returns dashes that span the print width at chrome size.
func formatTearline() string {
	// Roughly 32 chars at chrome size fits the 384px width with margin.
	return strings.Repeat("- ", 16)
}

// drawHeader draws the timestamp at top of img and returns the y-advance.
func drawHeader(img *image.Gray, c Chrome, topY int) (int, error) {
	face, err := Face(WeightRegular, ChromeSizePx)
	if err != nil {
		return 0, err
	}
	m := face.Metrics()
	baseline := topY + HeaderMarginPx + m.Ascent.Ceil()
	run := []Run{{Text: formatHeader(c), W: WeightRegular, Size: ChromeSizePx}}
	if err := drawLine(img, run, 0, baseline, AlignRight, PrintWidthPx); err != nil {
		return 0, err
	}
	return HeaderMarginPx + (m.Ascent + m.Descent).Ceil(), nil
}

// drawFooter draws tearline at given y, returns y-advance (including feed gap).
func drawFooter(img *image.Gray, y int) (int, error) {
	face, err := Face(WeightRegular, ChromeSizePx)
	if err != nil {
		return 0, err
	}
	m := face.Metrics()
	baseline := y + FooterMarginPx + m.Ascent.Ceil()
	run := []Run{{Text: formatTearline(), W: WeightRegular, Size: ChromeSizePx}}
	if err := drawLine(img, run, 0, baseline, AlignCenter, PrintWidthPx); err != nil {
		return 0, err
	}
	return FooterMarginPx + (m.Ascent + m.Descent).Ceil(), nil
}
