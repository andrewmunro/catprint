// Embedded fonts. Only Regular weights are pulled in; the rest of the
// NotoEmoji weights in assets/ are ignored at compile time.
package render

import (
	_ "embed"
	"fmt"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

//go:embed assets/NotoSans-Regular.ttf
var notoSansRegularTTF []byte

//go:embed assets/NotoSans-Bold.ttf
var notoSansBoldTTF []byte

//go:embed assets/NotoEmoji-Regular.ttf
var notoEmojiTTF []byte

// Weight selects which TTF a face is cut from.
type Weight int

const (
	WeightRegular Weight = iota
	WeightBold
	WeightEmoji
)

type faceKey struct {
	w    Weight
	size float64
}

var (
	parsedSansRegular *opentype.Font
	parsedSansBold    *opentype.Font
	parsedEmoji       *opentype.Font
	parseOnce         sync.Once
	parseErr          error

	faceCache = map[faceKey]font.Face{}
	faceMu    sync.Mutex
)

func parseFonts() error {
	parseOnce.Do(func() {
		if parsedSansRegular, parseErr = opentype.Parse(notoSansRegularTTF); parseErr != nil {
			parseErr = fmt.Errorf("parse NotoSans-Regular: %w", parseErr)
			return
		}
		if parsedSansBold, parseErr = opentype.Parse(notoSansBoldTTF); parseErr != nil {
			parseErr = fmt.Errorf("parse NotoSans-Bold: %w", parseErr)
			return
		}
		if parsedEmoji, parseErr = opentype.Parse(notoEmojiTTF); parseErr != nil {
			parseErr = fmt.Errorf("parse NotoEmoji-Regular: %w", parseErr)
			return
		}
	})
	return parseErr
}

// Face returns a cached face at the given pixel size. DPI is fixed at 72 so
// the requested size is in pixels.
func Face(w Weight, sizePx float64) (font.Face, error) {
	if err := parseFonts(); err != nil {
		return nil, err
	}
	key := faceKey{w, sizePx}
	faceMu.Lock()
	defer faceMu.Unlock()
	if f, ok := faceCache[key]; ok {
		return f, nil
	}
	var src *opentype.Font
	switch w {
	case WeightRegular:
		src = parsedSansRegular
	case WeightBold:
		src = parsedSansBold
	case WeightEmoji:
		src = parsedEmoji
	default:
		return nil, fmt.Errorf("unknown weight %d", w)
	}
	face, err := opentype.NewFace(src, &opentype.FaceOptions{
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	faceCache[key] = face
	return face, nil
}
