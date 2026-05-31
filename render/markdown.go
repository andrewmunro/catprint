// Markdown → 1-bit bitmap renderer. Walks the goldmark AST and emits styled
// runs for each supported block type. The render budget is a tall scratch
// canvas; we crop down to actual content height before returning.
package render

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmext "github.com/yuin/goldmark/extension"
	gmastx "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Width of paper in pixels — mirrors printer.PrintWidthPx to avoid an import cycle.
const PrintWidthPx = 384

// Scratch canvas height. 8000px ≈ 1m of paper — well over any sane print.
const maxCanvasHeight = 8000

// renderMu serializes all text rendering. The cached opentype faces carry
// internal glyph buffers that are not safe for concurrent use, and the queue
// worker, live preview, and history thumbnails can all render at once.
var renderMu sync.Mutex

// RenderMarkdown returns a 384px-wide grayscale bitmap of the rendered markdown
// with timestamp header and tearline footer auto-injected.
func RenderMarkdown(md string, c Chrome) (*image.Gray, error) {
	renderMu.Lock()
	defer renderMu.Unlock()

	canvas := image.NewGray(image.Rect(0, 0, PrintWidthPx, maxCanvasHeight))
	fillWhite(canvas)

	y := 0
	dy, err := drawHeader(canvas, c, y)
	if err != nil {
		return nil, err
	}
	y += dy

	bodyDY, err := drawBody(canvas, md, y)
	if err != nil {
		return nil, err
	}
	y += bodyDY

	dy, err = drawFooter(canvas, y)
	if err != nil {
		return nil, err
	}
	y += dy

	if y > maxCanvasHeight {
		y = maxCanvasHeight
	}
	sub := canvas.SubImage(image.Rect(0, 0, PrintWidthPx, y)).(*image.Gray)
	out := image.NewGray(image.Rect(0, 0, PrintWidthPx, y))
	for yy := 0; yy < y; yy++ {
		for xx := 0; xx < PrintWidthPx; xx++ {
			out.SetGray(xx, yy, sub.GrayAt(xx, yy))
		}
	}
	return out, nil
}

// drawBody parses the markdown and walks the top-level blocks.
func drawBody(img *image.Gray, md string, startY int) (int, error) {
	gm := goldmark.New(goldmark.WithExtensions(gmext.TaskList))
	root := gm.Parser().Parse(text.NewReader([]byte(md)))

	y := startY
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		dy, err := drawBlock(img, n, []byte(md), y)
		if err != nil {
			return y - startY, err
		}
		y += dy
		if y >= maxCanvasHeight {
			break
		}
	}
	return y - startY, nil
}

func drawBlock(img *image.Gray, n ast.Node, src []byte, y int) (int, error) {
	switch b := n.(type) {
	case *ast.Heading:
		return drawHeading(img, b, src, y)
	case *ast.Paragraph:
		runs := inlineRuns(b, src, WeightRegular, BodySizePx)
		return drawWrappedBlock(img, runs, 0, AlignLeft, y)
	case *ast.List:
		return drawList(img, b, src, y)
	case *ast.FencedCodeBlock:
		return drawMonoBlock(img, codeBlockText(b, src), y)
	case *ast.CodeBlock:
		return drawMonoBlock(img, codeBlockText(b, src), y)
	case *ast.ThematicBreak:
		return drawRule(img, y), nil
	default:
		runs := inlineRuns(n, src, WeightRegular, BodySizePx)
		if len(runs) == 0 {
			return 0, nil
		}
		return drawWrappedBlock(img, runs, 0, AlignLeft, y)
	}
}

func drawHeading(img *image.Gray, h *ast.Heading, src []byte, y int) (int, error) {
	var sz float64
	var w Weight
	var align Align
	switch h.Level {
	case 1:
		sz, w, align = HeadingSizePx, WeightBold, AlignCenter
	default:
		sz, w, align = SubheadingSizePx, WeightBold, AlignLeft
	}
	runs := inlineRuns(h, src, w, sz)
	return drawWrappedBlock(img, runs, 0, align, y)
}

func drawList(img *image.Gray, list *ast.List, src []byte, y int) (int, error) {
	startY := y
	for li := list.FirstChild(); li != nil; li = li.NextSibling() {
		item, ok := li.(*ast.ListItem)
		if !ok {
			continue
		}
		prefix, _ := taskPrefix(item)
		if prefix == "" {
			prefix = "• "
		}
		runs := []Run{{Text: prefix, W: WeightRegular, Size: BodySizePx}}
		runs = append(runs, inlineRuns(item, src, WeightRegular, BodySizePx)...)
		dy, err := drawWrappedBlock(img, runs, BulletIndentPx, AlignLeft, y)
		if err != nil {
			return y - startY, err
		}
		y += dy
	}
	return y - startY, nil
}

// taskPrefix returns "☑ " / "☐ " for a task-list item, or "" if it isn't one.
// taskPrefix scans the item subtree for a TaskCheckBox node (tight lists
// wrap inlines in TextBlock; loose lists wrap them in Paragraph).
func taskPrefix(item *ast.ListItem) (string, bool) {
	var found *gmastx.TaskCheckBox
	_ = ast.Walk(item, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if tc, ok := n.(*gmastx.TaskCheckBox); ok {
			found = tc
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	if found == nil {
		return "", false
	}
	if found.IsChecked {
		return "[x] ", true
	}
	return "[ ] ", true
}

// MonoMaxSizePx / MonoMinSizePx bound the auto-scaled monospace size used for
// code/ASCII-art blocks. We shrink to fit the widest line in the print width,
// down to the minimum (below which it's unreadable, so we let it clip).
const (
	MonoMaxSizePx = 20
	MonoMinSizePx = 7
)

// codeBlockText extracts the raw text of a (fenced) code block, preserving
// every space and newline exactly.
func codeBlockText(n ast.Node, src []byte) string {
	var b []byte
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b = append(b, seg.Value(src)...)
	}
	return strings.TrimRight(string(b), "\n")
}

// drawMonoBlock renders verbatim text in a monospace face, no wrapping, no
// length clamp. The face size is chosen so the widest line fits the print
// width (bounded by MonoMin/MaxSizePx). Spaces are preserved, so columns line
// up — which is what ASCII art needs.
func drawMonoBlock(img *image.Gray, text string, y int) (int, error) {
	if text == "" {
		return 0, nil
	}
	lines := strings.Split(text, "\n")
	widest := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > widest {
			widest = n
		}
	}
	if widest == 0 {
		widest = 1
	}

	// Find the largest size whose advance*widest fits PrintWidthPx.
	size := float64(MonoMaxSizePx)
	for ; size >= MonoMinSizePx; size-- {
		face, err := Face(WeightMono, size)
		if err != nil {
			return 0, err
		}
		adv, _ := face.GlyphAdvance('M') // monospace: every glyph same advance
		if adv.Ceil()*widest <= PrintWidthPx {
			break
		}
	}
	if size < MonoMinSizePx {
		size = MonoMinSizePx
	}

	face, err := Face(WeightMono, size)
	if err != nil {
		return 0, err
	}
	m := face.Metrics()
	lh := (m.Ascent + m.Descent).Ceil()
	startY := y
	for _, l := range lines {
		baseline := y + m.Ascent.Ceil()
		// Draw each line verbatim in mono — spaces included.
		if err := drawLine(img, []Run{{Text: l, W: WeightMono, Size: size}}, 0, baseline, AlignLeft, PrintWidthPx); err != nil {
			return y - startY, err
		}
		y += lh
	}
	return y - startY + LineGapPx, nil
}

// drawRule draws a full-width horizontal line and returns total y consumed.
func drawRule(img *image.Gray, y int) int {
	top := y + RuleMarginPx
	for dy := 0; dy < RuleHeightPx; dy++ {
		for x := 0; x < PrintWidthPx; x++ {
			img.SetGray(x, top+dy, color.Gray{Y: 0})
		}
	}
	return RuleMarginPx*2 + RuleHeightPx
}

// drawWrappedBlock wraps runs and draws each line at body size.
// indent shifts left edge. Returns total height consumed.
func drawWrappedBlock(img *image.Gray, runs []Run, indent int, align Align, y int) (int, error) {
	if len(runs) == 0 {
		return 0, nil
	}
	maxW := PrintWidthPx - indent
	lines, err := wordWrapRuns(runs, maxW)
	if err != nil {
		return 0, err
	}
	startY := y
	for _, line := range lines {
		var sz float64 = BodySizePx
		var w Weight = WeightRegular
		if len(line) > 0 {
			sz = line[0].Size
			w = line[0].W
		}
		lh, err := runHeight(w, sz)
		if err != nil {
			return y - startY, err
		}
		face, err := Face(w, sz)
		if err != nil {
			return y - startY, err
		}
		baseline := y + face.Metrics().Ascent.Ceil()
		if err := drawLine(img, line, indent, baseline, align, PrintWidthPx); err != nil {
			return y - startY, err
		}
		y += lh + LineGapPx
	}
	return y - startY, nil
}

// inlineRuns walks a block node's inline children and emits styled Runs.
// Bold spans switch weight to bold while preserving size.
func inlineRuns(node ast.Node, src []byte, baseW Weight, baseSize float64) []Run {
	var runs []Run
	var walk func(n ast.Node, w Weight)
	walk = func(n ast.Node, w Weight) {
		switch x := n.(type) {
		case *ast.Text:
			txt := string(x.Segment.Value(src))
			if txt != "" {
				runs = append(runs, Run{Text: txt, W: w, Size: baseSize})
			}
			// Hard break (line ended with two spaces / explicit break) forces a
			// new line; soft break is normal markdown flow → a space.
			if x.HardLineBreak() {
				runs = append(runs, Run{Text: "\n", W: w, Size: baseSize})
			} else if x.SoftLineBreak() {
				runs = append(runs, Run{Text: " ", W: w, Size: baseSize})
			}
		case *ast.Emphasis:
			next := w
			if x.Level >= 2 {
				next = WeightBold
			}
			for c := x.FirstChild(); c != nil; c = c.NextSibling() {
				walk(c, next)
			}
		case *ast.String:
			runs = append(runs, Run{Text: string(x.Value), W: w, Size: baseSize})
		case *gmastx.TaskCheckBox:
			// Consumed by drawList's prefix.
		default:
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				walk(c, w)
			}
		}
	}
	for c := node.FirstChild(); c != nil; c = c.NextSibling() {
		walk(c, baseW)
	}
	return runs
}

// Sanity: PrintWidthPx must equal printer.PrintWidthPx.
func init() {
	if PrintWidthPx != 384 {
		panic(fmt.Sprintf("render.PrintWidthPx mismatch: %d", PrintWidthPx))
	}
}
