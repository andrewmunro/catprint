// Image → 1-bit bitmap for the printer. Resize to the print width preserving
// aspect, then Floyd–Steinberg error-diffusion dither to pure black/white.
package render

import (
	"fmt"
	"image"
	"image/color"

	xdraw "golang.org/x/image/draw"
)

// MaxImageHeightPx caps a dithered image's height so a single photo can't eat
// the whole roll. ~1500px ≈ 19cm at 203dpi.
const MaxImageHeightPx = 1500

// Dither resizes src to the given width (preserving aspect), converts to
// grayscale, and applies Floyd–Steinberg dithering to produce a 1-bit image
// (pixels are exactly 0 or 255). Width defaults to PrintWidthPx if <= 0.
func Dither(src image.Image, width int) (*image.Gray, error) {
	if width <= 0 {
		width = PrintWidthPx
	}
	b := src.Bounds()
	if b.Empty() {
		return nil, fmt.Errorf("dither: empty image")
	}

	// Target height preserves aspect ratio.
	height := int(float64(width) * float64(b.Dy()) / float64(b.Dx()))
	if height < 1 {
		height = 1
	}
	if height > MaxImageHeightPx {
		height = MaxImageHeightPx
	}

	// High-quality downscale into a grayscale buffer.
	resized := image.NewGray(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(resized, resized.Bounds(), src, b, xdraw.Over, nil)

	floydSteinberg(resized)
	return resized, nil
}

// floydSteinberg quantises a grayscale image in place to 0/255 using
// Floyd–Steinberg error diffusion. Work in a float buffer so diffused error
// doesn't clip prematurely.
func floydSteinberg(img *image.Gray) {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	buf := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf[y*w+x] = float64(img.GrayAt(x, y).Y)
		}
	}

	at := func(x, y int) *float64 {
		if x < 0 || x >= w || y < 0 || y >= h {
			return nil
		}
		return &buf[y*w+x]
	}
	add := func(x, y, err float64) {
		if p := at(int(x), int(y)); p != nil {
			*p += err
		}
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			old := buf[y*w+x]
			var newv float64
			if old < 128 {
				newv = 0
			} else {
				newv = 255
			}
			buf[y*w+x] = newv
			qerr := old - newv
			// Standard FS weights: 7/16 right, 3/16 below-left,
			// 5/16 below, 1/16 below-right.
			add(float64(x+1), float64(y), qerr*7.0/16.0)
			add(float64(x-1), float64(y+1), qerr*3.0/16.0)
			add(float64(x), float64(y+1), qerr*5.0/16.0)
			add(float64(x+1), float64(y+1), qerr*1.0/16.0)
		}
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := buf[y*w+x]
			if v < 128 {
				img.SetGray(x, y, color.Gray{Y: 0})
			} else {
				img.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
}
