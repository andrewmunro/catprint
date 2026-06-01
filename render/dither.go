// Image → 1-bit bitmap for the printer. Resize to the print width preserving
// aspect, adjust tone, then reduce to pure black/white via the chosen mode
// (Floyd–Steinberg / Atkinson error diffusion, or a hard threshold).
package render

import (
	"fmt"
	"image"

	xdraw "golang.org/x/image/draw"
)

// MaxImageHeightPx caps a dithered image's height so a single photo can't eat
// the whole roll. ~1500px ≈ 19cm at 203dpi.
const MaxImageHeightPx = 1500

// DitherMode selects the black/white reduction algorithm.
type DitherMode string

const (
	ModeFloyd     DitherMode = "floyd"     // Floyd–Steinberg diffusion (default, smooth photos)
	ModeAtkinson  DitherMode = "atkinson"  // Atkinson diffusion (higher contrast, less muddy)
	ModeThreshold DitherMode = "threshold" // hard cut, no diffusion (line art, logos, text)
)

// ImageOptions controls the photo pipeline. The zero value is NOT valid — use
// DefaultImageOptions and override. Brightness/Contrast are -100..100 (0 = no
// change); Threshold is 0..255 and only used in ModeThreshold.
type ImageOptions struct {
	Mode       DitherMode
	Brightness int
	Contrast   int
	Threshold  int
	Width      int // <=0 defaults to PrintWidthPx
}

// DefaultImageOptions reproduces the historical behaviour exactly: Floyd–
// Steinberg, no tone adjustment. With these options Process is byte-identical
// to the old Dither, so the MCP / share paths are unaffected.
func DefaultImageOptions() ImageOptions {
	return ImageOptions{Mode: ModeFloyd, Brightness: 0, Contrast: 0, Threshold: 128}
}

// Dither is the default-params pipeline (Floyd–Steinberg, no tone change),
// kept for callers that don't expose options (MCP, share target).
func Dither(src image.Image, width int) (*image.Gray, error) {
	opts := DefaultImageOptions()
	opts.Width = width
	return Process(src, opts)
}

// Process resizes src to the target width (preserving aspect), applies
// brightness/contrast, then reduces to a 1-bit image (pixels exactly 0 or 255).
func Process(src image.Image, opts ImageOptions) (*image.Gray, error) {
	width := opts.Width
	if width <= 0 {
		width = PrintWidthPx
	}
	b := src.Bounds()
	if b.Empty() {
		return nil, fmt.Errorf("dither: empty image")
	}

	height := int(float64(width) * float64(b.Dy()) / float64(b.Dx()))
	if height < 1 {
		height = 1
	}
	if height > MaxImageHeightPx {
		height = MaxImageHeightPx
	}

	resized := image.NewGray(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(resized, resized.Bounds(), src, b, xdraw.Over, nil)

	adjustTone(resized, opts.Brightness, opts.Contrast)

	switch opts.Mode {
	case ModeAtkinson:
		atkinson(resized)
	case ModeThreshold:
		hardThreshold(resized, opts.Threshold)
	default: // ModeFloyd and unknown values
		floydSteinberg(resized)
	}
	return resized, nil
}

// adjustTone applies brightness then contrast in place. Brightness -100..100
// maps to ±128 grey levels; contrast -100..100 maps to a 0..2 multiplier about
// mid-grey. Both 0 is a no-op (the default path).
func adjustTone(img *image.Gray, brightness, contrast int) {
	if brightness == 0 && contrast == 0 {
		return
	}
	bShift := float64(brightness) * 128.0 / 100.0
	cFactor := float64(clampInt(contrast, -100, 100)+100) / 100.0 // 0..2
	for i, p := range img.Pix {
		v := float64(p) + bShift
		v = (v-128.0)*cFactor + 128.0
		img.Pix[i] = clampByte(v)
	}
}

// floydSteinberg quantises a grayscale image in place to 0/255 using
// Floyd–Steinberg error diffusion. Work in a float buffer so diffused error
// doesn't clip prematurely.
func floydSteinberg(img *image.Gray) {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	buf := toBuf(img)

	at := func(x, y int) *float64 {
		if x < 0 || x >= w || y < 0 || y >= h {
			return nil
		}
		return &buf[y*w+x]
	}
	add := func(x, y int, err float64) {
		if p := at(x, y); p != nil {
			*p += err
		}
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			old := buf[y*w+x]
			newv := quant(old)
			buf[y*w+x] = newv
			qerr := old - newv
			// Standard FS weights: 7/16 right, 3/16 below-left,
			// 5/16 below, 1/16 below-right.
			add(x+1, y, qerr*7.0/16.0)
			add(x-1, y+1, qerr*3.0/16.0)
			add(x, y+1, qerr*5.0/16.0)
			add(x+1, y+1, qerr*1.0/16.0)
		}
	}
	fromBuf(img, buf)
}

// atkinson quantises in place using Atkinson diffusion: only 6/8 of the error
// is spread (1/8 each to six neighbours), the rest discarded. Higher contrast
// and cleaner highlights than FS — usually better on thermal paper.
func atkinson(img *image.Gray) {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	buf := toBuf(img)

	add := func(x, y int, err float64) {
		if x < 0 || x >= w || y < 0 || y >= h {
			return
		}
		buf[y*w+x] += err
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			old := buf[y*w+x]
			newv := quant(old)
			buf[y*w+x] = newv
			e := (old - newv) / 8.0
			add(x+1, y, e)
			add(x+2, y, e)
			add(x-1, y+1, e)
			add(x, y+1, e)
			add(x+1, y+1, e)
			add(x, y+2, e)
		}
	}
	fromBuf(img, buf)
}

// hardThreshold reduces to black/white at a fixed cutoff with no diffusion.
// Best for line art, logos, screenshots, and QR codes.
func hardThreshold(img *image.Gray, level int) {
	level = clampInt(level, 0, 255)
	for i, p := range img.Pix {
		if int(p) < level {
			img.Pix[i] = 0
		} else {
			img.Pix[i] = 255
		}
	}
}

func quant(v float64) float64 {
	if v < 128 {
		return 0
	}
	return 255
}

func toBuf(img *image.Gray) []float64 {
	buf := make([]float64, len(img.Pix))
	for i, p := range img.Pix {
		buf[i] = float64(p)
	}
	return buf
}

func fromBuf(img *image.Gray, buf []float64) {
	for i, v := range buf {
		if v < 128 {
			img.Pix[i] = 0
		} else {
			img.Pix[i] = 255
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampByte(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
