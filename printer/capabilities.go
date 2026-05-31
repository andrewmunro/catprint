package printer

// Capability constants for the PD01 thermal printer.
// Mirrors the JSON returned by the MCP get_printer_capabilities tool.
const (
	PaperWidthMM       = 58
	PrintWidthPx       = 384
	MaxLineLengthChars = 32
	HeadingMaxChars    = 20
	BytesPerLine       = 48 // 384 / 8
)

var SupportedMarkdown = []string{
	"# heading (large bold centred, max 20 chars)",
	"## subheading (medium bold left)",
	"- bullet",
	"- [ ] checkbox empty",
	"- [x] checkbox checked",
	"**bold**",
	"--- full-width divider",
	"plain paragraphs (auto word-wrapped at 32 chars)",
	"emoji 🎉 (monochrome, rendered via Noto Emoji — encouraged for visual interest)",
}

var UnsupportedMarkdown = []string{
	"tables", "inline images", "colour", "links", "code blocks", "nested lists",
}

const AutoAddedNote = "timestamp header and tearline footer — do not add these yourself"

const CapabilitiesNotes = "All lines hard-clamped to 32 chars. Headings hard-clamped to 20 chars. " +
	"Emoji are supported and encouraged — they render as crisp monochrome glyphs and make " +
	"notes, lists, and headings more scannable. Each emoji counts as one character toward the " +
	"32-char line limit. Server returns line-specific errors on violations so you can correct and retry."

// Capabilities is the wire-format struct returned by the MCP tool.
type Capabilities struct {
	PaperWidthMM       int      `json:"paper_width_mm"`
	PrintWidthPx       int      `json:"print_width_px"`
	MaxLineLengthChars int      `json:"max_line_length_chars"`
	HeadingMaxChars    int      `json:"heading_max_chars"`
	SupportedMarkdown  []string `json:"supported_markdown"`
	Unsupported        []string `json:"unsupported"`
	AutoAddedByServer  string   `json:"auto_added_by_server"`
	Notes              string   `json:"notes"`
}

func CurrentCapabilities() Capabilities {
	return Capabilities{
		PaperWidthMM:       PaperWidthMM,
		PrintWidthPx:       PrintWidthPx,
		MaxLineLengthChars: MaxLineLengthChars,
		HeadingMaxChars:    HeadingMaxChars,
		SupportedMarkdown:  SupportedMarkdown,
		Unsupported:        UnsupportedMarkdown,
		AutoAddedByServer:  AutoAddedNote,
		Notes:              CapabilitiesNotes,
	}
}
