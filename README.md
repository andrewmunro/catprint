# catprint

Go server + MCP tools to drive a PD01 BLE thermal printer from Claude, scripts, or HTTP.

## Status

| Phase | What | Done |
| --- | --- | --- |
| 1 | PD01 BLE driver, SQLite job queue | ✓ |
| 2 | Markdown renderer + validator | ✓ |
| 3 | MCP server (4 tools, HTTP transport) | ✓ |
| 4 | Web UI + Android PWA | — |
| 5 | Voice + Python Workspace sidecar | — |
| 6 | Systemd + Cloudflare tunnel | — |
| 7 | Android Print Service APK | — |
| 8 | Image printing (Floyd–Steinberg) | — |

## Build

```bash
go mod tidy
go build ./...                                        # linux
GOOS=windows GOARCH=amd64 go build -o bin/catprint.exe .
```

Pure Go (modernc.org/sqlite). No CGO, no mingw needed.

## Run

```bash
./bin/catprint -addr D1:01:04:14:52:B4 -mcp-port 9000
# or rely on .printer_addr / scan:
./bin/catprint
```

Flags / env:

| Flag | Env | Default |
| --- | --- | --- |
| `-addr` | `PRINTER_ADDRESS` | scan + cache to `.printer_addr` |
| `-mcp-port` | `MCP_PORT` | `9000` |
| `-db` | `DB_PATH` | `jobs.db` |
| `-keepalive` | — | `20s` (0 = reconnect per job) |

The keepalive ticker holds the BLE connection open and sends a `GetDevState` ping at that interval to defeat the printer's idle sleep.

## MCP tools

`POST http://<host>:9000/mcp` (streamable HTTP transport):

| Tool | Purpose |
| --- | --- |
| `get_printer_capabilities` | Paper size, line limits, supported markdown subset. Call this first. |
| `get_printer_status` | Reachability, queue depth, last job state. |
| `print_markdown` | Validate → render → enqueue → wait → return `{job_id, status}`. |
| `print_image` | Stub (Phase 8). |

`print_markdown` returns an `IsError` result with `{error: "validation_failed", violations: [...]}` if the markdown violates the constraints. The LLM should call `get_printer_capabilities`, correct the line, and retry.

## Claude Desktop config

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "catprint": {
      "type": "http",
      "url": "http://localhost:9000/mcp"
    }
  }
}
```

For a remote server (later phases with Cloudflare tunnel):

```json
{
  "mcpServers": {
    "catprint": {
      "type": "http",
      "url": "https://your-tunnel.example.com/mcp"
    }
  }
}
```

Restart Claude Desktop. Open the tools menu; you should see four `catprint` tools.

## Quick test from curl

```bash
SESSION=$(curl -s -D - -X POST -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}' \
  http://localhost:9000/mcp | grep -i mcp-session-id | awk '{print $2}' | tr -d '\r')

curl -s -X POST -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -H "Mcp-Session-Id: $SESSION" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' http://localhost:9000/mcp

curl -s -X POST -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -H "Mcp-Session-Id: $SESSION" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"print_markdown","arguments":{"content":"# Hi\n- [ ] one\n- [x] two"}}}' \
  http://localhost:9000/mcp
```

## Diagnostic scripts

Standalone tools under `scripts/` — build with `go build ./scripts/<name>`. They talk to the printer directly and do not need the server running.

| Script | Use |
| --- | --- |
| `scan` | List BLE devices (find the printer's MAC). One-time setup. |
| `test_print` | Print a 384×60 black rectangle (driver/protocol smoke test). |
| `test_render` | Render the example docs to PNG (no printer) to eyeball fonts and layout. |

## Examples

`examples/` contains small markdown files you can feed to the web UI textarea or the `print_markdown` MCP tool.

## Repository layout

```
printer/      PD01 BLE driver + queue (Phase 1)
jobs/         SQLite job log (Phase 1)
render/       Markdown → 1-bit bitmap, font embed (Phase 2)
validate/     Pre-render markdown validator (Phase 2)
mcp/          MCP tool definitions (Phase 3)
scripts/      Standalone CLI tools
examples/     Sample markdown
```
