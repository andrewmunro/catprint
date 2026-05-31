// Job content dispatch. A print job's stored content is either markdown or a
// base64 data URI of an image; RenderContent picks the right pipeline so the
// queue, MCP, and web paths all behave identically.
package render

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	_ "golang.org/x/image/bmp"
)

// imageDataPrefix marks an image job. Format: data:image/<type>;base64,<data>
const imageDataPrefix = "data:image/"

// IsImageJob reports whether content is an image data URI rather than markdown.
func IsImageJob(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), imageDataPrefix)
}

// EncodeImageJob wraps raw base64 PNG/JPEG data in a data URI for storage.
func EncodeImageJob(mime, base64Data string) string {
	return fmt.Sprintf("data:%s;base64,%s", mime, base64Data)
}

// RenderContent renders a job to a 1-bit bitmap, dispatching on content type.
// Markdown gets the timestamp/tearline chrome; images are dithered as-is.
func RenderContent(content string, c Chrome) (*image.Gray, error) {
	if IsImageJob(content) {
		return renderImageDataURI(content)
	}
	return RenderMarkdown(content, c)
}

// NormalizeImageInput accepts either a bare base64 string or a full data: URI
// (any image type), verifies the bytes decode as a supported image, and returns
// a canonical data URI suitable for storing as job content. The detected image
// format is used as the mime subtype.
func NormalizeImageInput(input string) (string, error) {
	input = strings.TrimSpace(input)
	b64 := input
	if strings.HasPrefix(input, "data:") {
		comma := strings.IndexByte(input, ',')
		if comma < 0 {
			return "", fmt.Errorf("malformed data URI")
		}
		b64 = input[comma+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	_, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("unsupported or corrupt image: %w", err)
	}
	return EncodeImageJob("image/"+format, base64.StdEncoding.EncodeToString(raw)), nil
}

func renderImageDataURI(uri string) (*image.Gray, error) {
	uri = strings.TrimSpace(uri)
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return nil, fmt.Errorf("malformed image data URI")
	}
	raw, err := base64.StdEncoding.DecodeString(uri[comma+1:])
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return Dither(img, PrintWidthPx)
}
