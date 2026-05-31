# Thermal Printer Home Hub — Claude Code Plan

## Final Spec (post design interview)

---

## Agreed Design Decisions

Every decision below was explicitly agreed during the design interview. Do not deviate from these without a documented reason.

| # | Decision | Choice | Rationale |
| --- | --- | --- | --- |
| 1 | BLE connection model | Connect-on-demand | Printer sleeps when idle; persistent connection is fragile |
| 2 | Failed/offline jobs | Persist and retry (1hr expiry) | Silent failure is bad UX for voice and cron jobs |
| 3 | Markdown renderer | goldmark AST + fogleman/gg | Full control, ~300 lines, no headless browser |
| 4 | Body font | Noto Sans Regular + Bold | Best hinting at 203dpi thermal |
| 5 | Emoji fallback | Noto Emoji (monochrome) | LLMs emit emoji naturally; should just work |
| 6 | Capabilities | Injected into every system prompt AND validated server-side with actionable errors | Belt and braces; LLM self-corrects on retry |
| 7 | Job separation | Server auto-wraps every job with timestamp header + tearline footer | LLM owns content only; server owns paper chrome |
| 8 | Job log | SQLite, full markdown content stored, reprint button | Monitoring + recovery without push notifications |
| 9 | Language split | Go for printer core; Python subprocess for Workspace voice queries | WorkspaceExtensionConfig is Python-only in Gemini SDK |
| 10 | Go↔Python IPC | Subprocess stdin/stdout JSON lines | One systemd unit, one deployable, simpler ops |
| 11 | Google Home | Google Home Action (permanent dev/testing mode) + JWT verification skipped for now | Native voice, variable text, no IFTTT |
| 12 | Voice routing | "my" keyword heuristic → Python; everything else → Go + GoogleSearch | Zero latency, zero API cost routing |
| 13 | OAuth setup | One-time CLI script, token.json on Pi | Personal project, set once and forget |
| 14 | Auth | None for now | Home project, add later if needed |
| 15 | Paper-out detection | Not reliable on PD01; job status is "sent" not "printed" | Honest: BLE confirms bytes sent, not paper ejected |
| 16 | Print primitives | print_markdown and print_image only | Server accepts nothing else; all callers convert first |
| 17 | Image printing | Phase 8 — stubbed until then | Simple markdown first, complexity later |
| 18 | Android APK | Phase 7 — sideloaded debug build via ADB | No Play Store needed |

---

## Architecture

```
                    ┌─────────────────────────────────────┐
                    │         CALLERS                      │
                    │                                      │
                    │  Claude Desktop (MCP over HTTP)      │
                    │  Android PWA (share sheet)           │
                    │  Android Print Service APK           │
                    │  Google Home Action (webhook)        │
                    │  Browser web UI                      │
                    └──────────────┬──────────────────────┘
                                   │ HTTPS
                    ┌──────────────▼──────────────────────┐
                    │      Cloudflare Tunnel               │
                    │   (named tunnel, persistent URL)     │
                    └──────────────┬──────────────────────┘
                                   │
                    ┌──────────────▼──────────────────────┐
                    │         RASPBERRY PI                 │
                    │                                      │
                    │  ┌─────────────────────────────┐    │
                    │  │     Go binary (main)         │    │
                    │  │                              │    │
                    │  │  MCP server (HTTP transport) │    │
                    │  │  Web UI + API routes         │    │
                    │  │  Voice webhook handler       │    │
                    │  │  Job queue (SQLite)          │    │
                    │  │  Markdown renderer           │    │
                    │  │  PD01 BLE driver             │    │
                    │  └──────────┬──────────────────┘    │
                    │             │ stdin/stdout           │
                    │  ┌──────────▼──────────────────┐    │
                    │  │  Python subprocess           │    │
                    │  │  (Workspace voice queries)   │    │
                    │  │                              │    │
                    │  │  FastAPI + google-genai      │    │
                    │  │  WorkspaceExtensionConfig    │    │
                    │  │  OAuth token.json            │    │
                    │  └─────────────────────────────┘    │
                    │                                      │
                    └──────────────┬──────────────────────┘
                                   │ BLE (connect-on-demand)
                    ┌──────────────▼──────────────────────┐
                    │     PD01 Thermal Printer             │
                    │     58mm, 384px wide, 203dpi         │
                    └─────────────────────────────────────┘
```

---

## The Two Print Primitives

The server accepts exactly two inputs. All callers convert to one of these first.

### `print_markdown(content string) → "ok" | error`

Markdown string conforming to the supported subset. Server auto-prepends a timestamp header and appends a tearline footer. Rendered to a 384px-wide 1-bit bitmap and sent to the printer.

### `print_image(base64_png string) → "ok" | error`

_(Phase 8)_ Base64 PNG, resized to 384px wide, Floyd-Steinberg dithered to 1-bit. Until Phase 8: returns `"image printing not yet implemented"`.

---

## Supported Markdown Subset

| Element          | Syntax       | Rendered as                       |
| ---------------- | ------------ | --------------------------------- |
| Title            | `# Title`    | Large bold, centred, max 20 chars |
| Subheading       | `## Section` | Medium bold, left-aligned         |
| Bullet           | `- item`     | • item, indented                  |
| Checkbox empty   | `- [ ] item` | ☐ item                            |
| Checkbox checked | `- [x] item` | ☑ item                            |
| Bold             | `**text**`   | Bold weight                       |
| Divider          | `---`        | Full-width rule                   |
| Plain paragraph  | text         | Word-wrapped at 32 chars          |

**Unsupported:** tables, inline images, colour, links, code blocks, nested lists.

### Auto-injected chrome (server adds, LLM never writes)

```
┌─────────────────────────────┐
│ Sat 31 May · 07:32  [src]   │  ← timestamp header (small, right-aligned source tag)
├─────────────────────────────┤
│                             │
│   [LLM markdown content]    │
│                             │
├─────────────────────────────┤
│ - - - - - - - - - - - - -   │  ← tearline
└─────────────────────────────┘
                               ← 4 line feeds (expose tearline above printer mouth)
```

---

## MCP Tool Definitions

### `get_printer_capabilities`

```
Always call this before composing content for printing.
Returns paper dimensions, line length limits, supported markdown subset,
and formatting guidance. The server validates your markdown before printing
and will return actionable errors if constraints are violated.
```

Returns:

```json
{
	"paper_width_mm": 58,
	"print_width_px": 384,
	"max_line_length_chars": 32,
	"heading_max_chars": 20,
	"supported_markdown": [
		"# heading (large bold centred, max 20 chars)",
		"## subheading (medium bold left)",
		"- bullet",
		"- [ ] checkbox empty",
		"- [x] checkbox checked",
		"**bold**",
		"--- full-width divider",
		"plain paragraphs (auto word-wrapped at 32 chars)"
	],
	"unsupported": ["tables", "inline images", "colour", "links", "code blocks", "nested lists"],
	"auto_added_by_server": "timestamp header and tearline footer — do not add these yourself",
	"notes": "All lines hard-clamped to 32 chars. Headings hard-clamped to 20 chars. Server returns line-specific errors on violations so you can correct and retry."
}
```

### `get_printer_status`

```
Returns connection reachability and last known state.
Note: 'sent' means bytes were transmitted over BLE, not that paper was ejected.
Paper-out is not reliably detectable on this printer model.
```

Returns:

```json
{
	"reachable": true,
	"last_job_status": "sent",
	"queue_depth": 0,
	"jobs_pending_retry": 0
}
```

### `print_markdown`

```
Print markdown to the thermal printer.
Content must conform to get_printer_capabilities constraints.
Server validates before rendering and returns line-specific errors on violation.
Do not include timestamp or tearline — server adds these automatically.
Returns "ok" or a structured error.
```

Parameter: `content string`

Error format:

```json
{
	"error": "validation_failed",
	"violations": [{ "line": 3, "issue": "exceeds 32 chars", "actual": 38, "content": "This line is too long to fit on paper" }]
}
```

### `print_image`

_(Phase 8)_ — stub returns `"not yet implemented"`.

---

## Repository Structure

```
thermal-printer-hub/
├── main.go                        # Entry point
├── go.mod
├── go.sum
├── .env.example
├── README.md
│
├── printer/
│   ├── pd01.go                    # PD01 BLE driver (Phase 1)
│   ├── queue.go                   # Persistent job queue + retry (Phase 1)
│   └── capabilities.go            # Capability constants (Phase 1)
│
├── render/
│   ├── markdown.go                # Markdown AST → bitmap (Phase 2)
│   ├── layout.go                  # Word wrap, line spacing (Phase 2)
│   ├── fonts.go                   # Embedded Noto Sans + Noto Emoji (Phase 2)
│   ├── chrome.go                  # Timestamp header + tearline footer (Phase 2)
│   └── dither.go                  # Floyd-Steinberg for images (Phase 8)
│
├── validate/
│   └── markdown.go                # Pre-render validation, structured errors (Phase 2)
│
├── mcp/
│   └── server.go                  # MCP server, tool definitions (Phase 3)
│
├── web/
│   ├── server.go                  # HTTP routes (Phase 4)
│   └── static/
│       ├── index.html             # Mobile PWA (Phase 4)
│       ├── manifest.json          # Web Share Target (Phase 4)
│       └── sw.js                  # Service worker (Phase 4)
│
├── voice/
│   ├── handler.go                 # Google Home webhook, routing heuristic (Phase 5)
│   └── workspace.go               # Go↔Python subprocess manager (Phase 5)
│
├── jobs/
│   └── store.go                   # SQLite job log, reprint query (Phase 1)
│
├── workspace_sidecar/             # Python subprocess (Phase 5)
│   ├── main.py                    # stdin/stdout JSON lines handler
│   ├── gemini_client.py           # Gemini + WorkspaceExtensionConfig
│   ├── requirements.txt
│   └── setup_oauth.py             # One-time OAuth CLI script
│
├── android/                       # Android Print Service APK (Phase 7)
│   └── app/src/main/
│       ├── HomePrintService.kt
│       ├── PdfConverter.kt
│       └── PrinterClient.kt
│
├── scripts/
│   ├── test_print.go              # Smoke test: solid rectangle
│   ├── test_render.go             # Render sample docs to PNG for visual check
│   └── setup_oauth.sh             # Wrapper: runs workspace_sidecar/setup_oauth.py
│
└── systemd/
    ├── printer-hub.service        # Single unit: Go binary (spawns Python)
    └── printer-tunnel.service     # Cloudflare named tunnel
```

---

## Job Queue Design

```go
type Job struct {
    ID          string        // UUID
    CreatedAt   time.Time
    ExpiresAt   time.Time     // CreatedAt + 1 hour
    Source      string        // "mcp" | "web" | "voice" | "apk"
    Status      string        // "queued" | "sent" | "failed" | "expired"
    Content     string        // full markdown (for reprint)
    Error       string        // last error message if failed
    RetryCount  int
    SentAt      *time.Time    // when BLE bytes were transmitted
}
```

**Queue worker behaviour:**

- Single goroutine, polls queue every 5s
- On dequeue: BLE connect → render → send → mark "sent" → disconnect
- On BLE error: retry up to 3× with 2s backoff, then mark "failed"
- Background sweep every 60s: expire jobs older than 1 hour, mark "expired"
- Printer MAC cached in `.printer_addr` after first successful discovery

**Status semantics (documented in UI):**

- `queued` — waiting to print
- `sent` — bytes transmitted over BLE (≠ paper ejected — check printer)
- `failed` — BLE error after 3 retries, reprint available
- `expired` — printer unreachable for 1 hour, job discarded

---

## Voice Webhook Architecture

### Routing heuristic (Go, zero latency)

```go
func needsWorkspace(query string) bool {
    personal := []string{"my ", "my calendar", "my shopping", "my keep",
                         "my emails", "my tasks", "my reminders", "my notes"}
    q := strings.ToLower(query)
    for _, p := range personal {
        if strings.Contains(q, p) { return true }
    }
    return false
}
```

### Go path (GoogleSearch tool)

```go
// Uses google.golang.org/genai with GoogleSearch tool
// Handles: weather, directions, news, recipes, general knowledge
config := &genai.GenerateContentConfig{
    SystemInstruction: ..., // includes printer capabilities JSON
    Tools: []*genai.Tool{
        {FunctionDeclarations: []*genai.FunctionDeclaration{printTool}},
        {GoogleSearch: &genai.GoogleSearch{}},
    },
}
```

### Python path (WorkspaceExtensionConfig)

Go writes to Python subprocess stdin:

```json
{ "query": "my shopping list from Keep" }
```

Python calls Gemini with WorkspaceExtensionConfig, writes to stdout:

```json
{ "markdown": "# Shopping List\n\n- [ ] Apples\n- [ ] Milk" }
```

Go receives markdown, pipes to `print_markdown`.

### Python subprocess manager (`voice/workspace.go`)

- Go starts Python on first Workspace query (lazy init)
- Keeps process alive, restarts if it crashes (detected via broken pipe)
- 30s timeout per query; on timeout: kill + restart process, fail job

---

## Render Pipeline

```
Markdown string
      ↓
validate/markdown.go  →  structured errors if violations (return to caller)
      ↓ (valid)
render/chrome.go      →  prepend timestamp header block
      ↓
render/markdown.go    →  goldmark AST walk → gg drawing calls
      ↓
render/chrome.go      →  append tearline footer block
      ↓
*image.Gray (384px wide, 1-bit)
      ↓
printer/pd01.go       →  pack rows into PD01 packets → BLE write
      ↓
printer/queue.go      →  mark job "sent"
      ↓
jobs/store.go         →  update SQLite record
```

### Font stack

```
Primary:  Noto Sans Regular (body, 24px)
          Noto Sans Bold    (bold spans, subheadings, 24px)
          Noto Sans Bold    (headings, 48px)
Fallback: Noto Emoji        (monochrome variant, matched size)
All fonts embedded via //go:embed — zero runtime file dependencies
```

### Rendering rules per element

```
# Heading      → 48px Bold, centred, max 20 chars (hard clamp with ellipsis)
## Subheading  → 32px Bold, left, full width
- bullet       → 24px Regular, "• " prefix, 8px left indent
- [ ] checkbox → 24px Regular, "☐ " prefix (☑ if checked)
**bold**       → 24px Bold inline (switched mid-line)
---            → 2px horizontal rule, full width, 8px vertical margin
paragraph      → 24px Regular, word-wrapped at 384px, 4px line gap
```

### Chrome (auto-added, never in LLM content)

```
Header: "Sat 31 May · 07:32  [voice]"  →  18px Regular, right-aligned, 12px top margin
Footer: "- - - - - - - - - - - - - -"  →  18px Regular, centred, 12px top margin
Feed:   4 blank lines after tearline
```

---

## Phase Plan

### Phase 1 — PD01 Driver + Job Queue

**Goal:** Go connects to printer, prints a test rectangle, queue persists jobs to SQLite.

Tasks:

1. `printer/capabilities.go` — capability constants matching MCP JSON
2. `printer/pd01.go` — vendor/adapt rhnvrm/catprinter, port CRC8 exactly
    - `Discover(ctx) (string, error)`
    - `Connect(ctx, addr string) (*Printer, error)`
    - `PrintBitmap(ctx, img *image.Gray) error`
    - `Feed(ctx, lines int) error`
3. `printer/queue.go` — buffered channel, single worker goroutine, 3× retry
4. `jobs/store.go` — SQLite schema, CRUD, expiry sweep
5. `scripts/test_print.go` — discover → connect → print solid 384×60 black rectangle → feed

**Milestone:** solid black rectangle on paper.

---

### Phase 2 — Markdown Renderer + Validator

**Goal:** Markdown string → PNG file for visual inspection. No printing yet.

Tasks:

1. `render/fonts.go` — embed Noto Sans (Regular + Bold) + Noto Emoji monochrome
2. `render/layout.go` — WordWrap, DrawText, spacing constants
3. `render/markdown.go` — goldmark AST walk, gg drawing calls per element type
4. `render/chrome.go` — timestamp header, tearline footer, feed
5. `validate/markdown.go` — line length check, heading length check, unsupported element detection, structured error format
6. `scripts/test_render.go` — render grocery list + itinerary examples → save as PNG

**Milestone:** visually inspect PNG output. Tune font sizes and spacing before any paper is used.

Wire renderer into queue worker. Print the grocery list example on real paper.

**Milestone:** readable grocery list on paper. Tune until it looks good.

---

### Phase 3 — MCP Server

**Goal:** Claude Desktop on laptop can print a todo list.

Tasks:

1. `mcp/server.go` — Go MCP SDK, HTTP transport, 4 tools
2. Capabilities injected into every system prompt (not just available as a tool)
3. `print_markdown` validates before rendering, returns structured errors
4. `print_image` stub returns "not yet implemented"
5. README: Claude Desktop config snippet

**⛳ Milestone — use it daily before continuing.** Print todo lists, shopping lists, notes from Claude Desktop. What formatting looks bad on real paper? Fix the renderer before adding surface area.

---

### Phase 4 — Web UI + Android PWA

**Goal:** Print from phone browser. Appear in Android share sheet.

Tasks:

1. `web/server.go` — routes: GET /, POST /print/text, POST /print/share, GET /status, GET /jobs, POST /jobs/:id/reprint
2. `web/static/index.html` — mobile-first, textarea + print button, job history log with status and reprint button
3. `web/static/manifest.json` — Web Share Target (text + image)
4. `web/static/sw.js` — service worker for PWA install

**Job history UI shows:**

- Timestamp, source tag, first line of content, status badge
- Reprint button for failed/sent jobs
- Status note: "sent = BLE transmitted, not paper confirmed"

---

### Phase 5 — Voice (Google Home + Python Workspace Sidecar)

**Goal:** "Hey Google, print my shopping list" → paper.

Tasks:

1. `workspace_sidecar/main.py` — stdin/stdout JSON lines loop
2. `workspace_sidecar/gemini_client.py` — Gemini + WorkspaceExtensionConfig + OAuth
3. `workspace_sidecar/setup_oauth.py` — one-time CLI OAuth flow, saves token.json
4. `voice/workspace.go` — subprocess manager (lazy init, crash restart, 30s timeout)
5. `voice/handler.go` — Google Home webhook route, routing heuristic, Go Gemini path
6. Wire both paths into `print_markdown`

**Google Home Action setup (document in README):**

- Google Cloud project → Actions Console → conversational action
- Webhook: `POST https://your-tunnel.trycloudflare.com/voice`
- Body: `{"query": "{{spokenText}}"}`
- Keep in developer/testing mode permanently

**OAuth setup (document in README):**

```bash
./scripts/setup_oauth.sh
# Opens browser → sign in with Google → token.json saved to workspace_sidecar/
```

---

### Phase 6 — Infrastructure

**Goal:** Everything starts on boot, is reachable publicly, easy to update.

Tasks:

1. `Makefile` — `build-pi`, `deploy`, `test-print`, `logs`
2. `systemd/printer-hub.service` — single unit, Go binary, `EnvironmentFile`
3. `systemd/printer-tunnel.service` — named Cloudflare tunnel
4. `.env.example` — all config vars documented
5. Full README: Pi setup, Bluetooth pairing, Cloudflare tunnel, OAuth, Google Home Action, Claude Desktop config

```makefile
build-pi:
    GOARCH=arm64 GOOS=linux go build -o bin/printer-hub .

deploy:
    scp bin/printer-hub pi@raspberrypi.local:~/thermal-printer-hub/bin/
    ssh pi@raspberrypi.local sudo systemctl restart printer-hub

test-print:
    curl -s -X POST http://raspberrypi.local:8080/print/text \
      -H "Content-Type: application/json" \
      -d '{"content":"- [ ] Apples\n- [ ] Milk\n- [ ] Eggs","title":"SHOPPING LIST"}'

logs:
    ssh pi@raspberrypi.local journalctl -u printer-hub -f
```

```ini
# systemd/printer-hub.service
[Unit]
Description=Thermal Printer Hub
After=network.target bluetooth.target

[Service]
ExecStart=/home/pi/thermal-printer-hub/bin/printer-hub
Restart=always
RestartSec=5
EnvironmentFile=/home/pi/thermal-printer-hub/.env
User=pi
WorkingDirectory=/home/pi/thermal-printer-hub

[Install]
WantedBy=multi-user.target
```

```
# .env.example
WEB_PORT=8080
MCP_PORT=9000

GOOGLE_API_KEY=
PRINTER_ADDRESS=          # BLE MAC — auto-discovered if blank
DIGEST_LOCATION=London, UK

PYTHON_BIN=/home/pi/thermal-printer-hub/workspace_sidecar/venv/bin/python
WORKSPACE_SIDECAR=./workspace_sidecar/main.py
```

---

### Phase 7 — Android Print Service APK

**Goal:** Print from any Android app natively via system print dialog.

Tasks:

1. `HomePrintService.kt` — PrintService subclass, registers in manifest
2. `PdfConverter.kt` — per-page: text extraction heuristic → POST /print/text; image-rich → POST /print/image (Phase 8 stub)
3. `PrinterClient.kt` — OkHttp, server URL + no auth (Phase 6 decision)
4. Settings Activity — server URL input, connection test button
5. Sideload: `./gradlew assembleDebug && adb install app-debug.apk`
6. `android/README.md` — sideload instructions, Settings → Printing → Enable

**Page routing heuristic in PdfConverter:**

- Extract text via PdfRenderer
- If `extractedText.length > 100 && imageAreaPct < 50%` → text path → POST /print/text
- Otherwise → image path → POST /print/image (returns stub until Phase 8)
- Multi-page docs: print each page sequentially, 2-line feed gap between pages

---

### Phase 8 — Image Printing

**Goal:** Photos, QR codes, maps, image-rich PDF pages print correctly.

Tasks:

1. `render/dither.go` — `Dither(src image.Image, width int) *image.Gray` — resize + Floyd-Steinberg
2. Wire `print_image` in MCP and web server (remove stub)
3. Update `PdfConverter.kt` — image path now sends actual PNG instead of stub

---

## Key Dependencies

### Go

```
google.golang.org/genai              # Gemini API (Go SDK)
github.com/modelcontextprotocol/go-sdk  # MCP server
github.com/yuin/goldmark             # Markdown AST parser
github.com/fogleman/gg               # 2D drawing context
golang.org/x/image/font              # Font rendering
github.com/rhnvrm/catprinter         # PD01 BLE driver (vendor)
github.com/mattn/go-sqlite3          # SQLite
github.com/google/uuid               # Job IDs
tinygo.org/x/bluetooth               # BLE (used by catprinter)
```

### Python (workspace_sidecar only)

```
google-genai                         # Gemini SDK with WorkspaceExtensionConfig
google-auth-oauthlib                 # OAuth flow
google-auth                          # Credential management
```

### Android APK

```
com.squareup.okhttp3:okhttp
org.jetbrains.kotlinx:kotlinx-coroutines-android
```

---

## Claude Code Session Prompts

```bash
# Session 1 — PD01 driver + job queue
"Implement Phase 1 of the thermal printer hub.
 Vendor https://github.com/rhnvrm/catprinter into printer/.
 Port CRC8 and all command constants exactly from pd01.go — do not guess.
 Implement the job queue with SQLite persistence using go-sqlite3.
 Job states: queued → sent | failed | expired. Expiry after 1 hour.
 Write scripts/test_print.go: discover printer named PD01, connect,
 print a solid 384×60 black rectangle, feed 4 lines, disconnect."

# Session 2 — Markdown renderer + validator
"Implement Phase 2.
 Embed Noto Sans (Regular + Bold) and Noto Emoji monochrome using go:embed.
 Use goldmark for AST parsing and fogleman/gg for drawing.
 Implement all elements in the plan's rendering rules table.
 Implement validate/markdown.go with structured error format.
 Write scripts/test_render.go: render the grocery list and itinerary
 examples from the plan, save as test_grocery.png and test_itinerary.png.
 Do not wire into the printer yet — visual inspection first."

# Session 3 — MCP server
"Implement Phase 3.
 Use the Go MCP SDK with HTTP transport on MCP_PORT.
 Implement get_printer_capabilities, get_printer_status,
 print_markdown (validate → render → queue → wait → return),
 and print_image stub returning 'not yet implemented'.
 Inject capability JSON into system prompt of every Gemini call.
 Add Claude Desktop config to README."

# Session 4 — Web UI + PWA
"Implement Phase 4.
 Web server on WEB_PORT with routes: GET /, POST /print/text,
 POST /print/share, GET /status, GET /jobs, POST /jobs/:id/reprint.
 Mobile-first single-page UI with textarea, print button, job history.
 Job history shows: timestamp, source, first line, status badge, reprint button.
 Include status note: 'sent = BLE transmitted, not paper confirmed'.
 Register as Android Web Share Target via manifest.json."

# Session 5 — Voice + Python workspace sidecar
"Implement Phase 5.
 Python sidecar: stdin/stdout JSON lines, Gemini with WorkspaceExtensionConfig,
 OAuth via token.json. Script setup_oauth.py for one-time CLI auth flow.
 Go subprocess manager: lazy init, crash detection and restart, 30s timeout.
 Voice webhook handler: routing heuristic (contains 'my' → Python path,
 else → Go Gemini path with GoogleSearch tool).
 Both paths format markdown then call print_markdown.
 Document Google Home Action setup and OAuth setup in README."

# Session 6 — Infrastructure
"Implement Phase 6.
 Makefile: build-pi (GOARCH=arm64 GOOS=linux), deploy (scp + restart), test-print, logs.
 Systemd units: printer-hub.service (single unit, Go spawns Python),
 printer-tunnel.service (Cloudflare named tunnel).
 Complete README: Pi setup, Bluetooth pairing, Cloudflare, OAuth, Google Home,
 Claude Desktop config, all env vars documented."

# Session 7 — Android APK
"Implement Phase 7.
 Kotlin Android app with PrintService subclass (HomePrintService).
 PdfConverter: per-page text extraction heuristic, POST to server.
 Text path: POST /print/text. Image path: POST /print/image (stub ok).
 Multi-page: sequential with 2-line feed gap.
 Settings Activity: server URL input, test connection button.
 Include sideload instructions in android/README.md."

# Session 8 — Image printing
"Implement Phase 8.
 render/dither.go: Floyd-Steinberg dither to 1-bit at 384px wide.
 Wire print_image in MCP server and web server — remove stub.
 Update PdfConverter in APK: image path sends actual PNG."
```

---

## Risks & Mitigations

| Risk                                 | Mitigation                                                           |
| ------------------------------------ | -------------------------------------------------------------------- |
| PD01 CRC8 polynomial wrong           | Port exactly from rhnvrm/catprinter; test with solid rectangle       |
| BLE drops mid-print                  | Queue retries 3×; bitmap already generated, safe to resend           |
| Renderer looks bad on paper          | Phase 2 saves PNG for visual review before printing                  |
| Cloudflare URL changes               | Named tunnel = persistent subdomain                                  |
| LLM writes lines > 32 chars          | Validator returns line-specific error; LLM self-corrects and retries |
| Concurrent BLE access                | Single queue goroutine — all BLE serialised                          |
| Python sidecar crashes               | Go detects broken pipe, restarts process, fails current job cleanly  |
| WorkspaceExtensionConfig unavailable | Already confirmed working in Python SDK with OAuth credentials       |
| APK text extraction yields garbage   | Heuristic falls back to image path                                   |
| Google Home Action review required   | Stay in developer/testing mode permanently — personal use only       |
| emoji in LLM output                  | Noto Emoji fallback font handles gracefully                          |
