// Layout primitives: pixel sizes, spacing, word wrap, run-emitter that
// switches between body and emoji faces per glyph.
package render

import (
	"image"
	"image/color"
	"image/draw"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// Pixel sizes from the plan.
const (
	HeadingSizePx    = 48
	SubheadingSizePx = 32
	BodySizePx       = 24
	ChromeSizePx     = 18

	BulletIndentPx = 8
	LineGapPx      = 4
	RuleHeightPx   = 2
	RuleMarginPx   = 8
	HeaderMarginPx = 2
	FooterMarginPx = 12
)

// Run is a styled chunk of text on a single line.
type Run struct {
	Text string
	W    Weight
	Size float64
}

// pickFace returns the right face for the run, falling back to the emoji face
// for any rune outside Basic Multilingual Plane Latin/punctuation range that
// the body font is missing.
func pickFaceForRune(r rune, bodyFace, emojiFace font.Face) font.Face {
	if _, ok := bodyFace.GlyphAdvance(r); ok {
		return bodyFace
	}
	return emojiFace
}

// measureRuneAdvance returns the horizontal advance for one rune in the given face.
func measureRuneAdvance(f font.Face, r rune) fixed.Int26_6 {
	adv, _ := f.GlyphAdvance(r)
	return adv
}

// measureRunWidth measures a run with per-glyph emoji fallback.
func measureRunWidth(run Run) (fixed.Int26_6, error) {
	body, err := Face(run.W, run.Size)
	if err != nil {
		return 0, err
	}
	emoji, err := Face(WeightEmoji, run.Size)
	if err != nil {
		return 0, err
	}
	var w fixed.Int26_6
	for _, r := range run.Text {
		f := pickFaceForRune(r, body, emoji)
		w += measureRuneAdvance(f, r)
	}
	return w, nil
}

// wordWrapRuns splits runs into lines that fit within maxWidthPx.
// Wrapping happens on whitespace; words longer than the line are emitted as-is.
func wordWrapRuns(runs []Run, maxWidthPx int) ([][]Run, error) {
	max := fixed.I(maxWidthPx)
	var lines [][]Run
	var current []Run
	var currentW fixed.Int26_6

	flush := func() {
		if len(current) > 0 {
			lines = append(lines, current)
			current = nil
			currentW = 0
		}
	}

	for _, run := range runs {
		// Split run into word/space tokens, preserving styling.
		tokens := splitWords(run.Text)
		for _, tok := range tokens {
			if tok == "\n" {
				flush()
				continue
			}
			tw, err := measureRunWidth(Run{Text: tok, W: run.W, Size: run.Size})
			if err != nil {
				return nil, err
			}
			// Skip leading whitespace at the start of a line.
			if currentW == 0 && isAllSpace(tok) {
				continue
			}
			if currentW+tw > max && currentW > 0 {
				flush()
				if isAllSpace(tok) {
					continue
				}
			}
			// Append (merging with previous run if styling matches).
			if n := len(current); n > 0 && current[n-1].W == run.W && current[n-1].Size == run.Size {
				current[n-1].Text += tok
			} else {
				current = append(current, Run{Text: tok, W: run.W, Size: run.Size})
			}
			currentW += tw
		}
	}
	flush()
	return lines, nil
}

func splitWords(s string) []string {
	var out []string
	var buf []rune
	inSpace := false
	flush := func() {
		if len(buf) > 0 {
			out = append(out, string(buf))
			buf = buf[:0]
		}
	}
	for _, r := range s {
		if r == '\n' {
			flush()
			out = append(out, "\n")
			inSpace = false
			continue
		}
		space := unicode.IsSpace(r)
		if space != inSpace {
			flush()
			inSpace = space
		}
		buf = append(buf, r)
	}
	flush()
	return out
}

func isAllSpace(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return len(s) > 0
}

// runHeight returns the line height for a single styled run (face metrics).
func runHeight(w Weight, sizePx float64) (int, error) {
	f, err := Face(w, sizePx)
	if err != nil {
		return 0, err
	}
	m := f.Metrics()
	return (m.Ascent + m.Descent).Ceil(), nil
}

// drawLine renders one wrapped line of runs at baseline y on img.
// Returns x advance.
func drawLine(img *image.Gray, runs []Run, x, baseline int, align Align, maxWidth int) error {
	if align != AlignLeft {
		w, err := lineWidth(runs)
		if err != nil {
			return err
		}
		switch align {
		case AlignCenter:
			x = (maxWidth - w.Ceil()) / 2
		case AlignRight:
			x = maxWidth - w.Ceil()
		}
		if x < 0 {
			x = 0
		}
	}
	pen := fixed.P(x, baseline)
	for _, run := range runs {
		body, err := Face(run.W, run.Size)
		if err != nil {
			return err
		}
		emoji, err := Face(WeightEmoji, run.Size)
		if err != nil {
			return err
		}
		for _, r := range run.Text {
			f := pickFaceForRune(r, body, emoji)
			drawer := &font.Drawer{
				Dst:  img,
				Src:  &image.Uniform{C: color.Black},
				Face: f,
				Dot:  pen,
			}
			drawer.DrawString(string(r))
			pen.X += measureRuneAdvance(f, r)
		}
	}
	return nil
}

func lineWidth(runs []Run) (fixed.Int26_6, error) {
	var w fixed.Int26_6
	for _, run := range runs {
		rw, err := measureRunWidth(run)
		if err != nil {
			return 0, err
		}
		w += rw
	}
	return w, nil
}

// Align controls horizontal alignment within the print width.
type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// fillWhite paints the entire image white. New image.Gray is zero-filled
// (black); we need white so black ink shows up correctly.
func fillWhite(img *image.Gray) {
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
}
