// MCP server exposing four tools: get_printer_capabilities, get_printer_status,
// print_markdown, print_image. Phase 3 of the plan.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/synestry/catprint/jobs"
	"github.com/synestry/catprint/printer"
	"github.com/synestry/catprint/validate"
)

// PrintWaitTimeout caps how long print_markdown blocks before returning the
// current job state. The queue keeps trying in the background regardless.
const PrintWaitTimeout = 30 * time.Second

// Deps is everything the tool handlers need from the rest of the app.
type Deps struct {
	Queue *printer.Queue
	Store *jobs.Store
}

// New returns an MCP server with the four printer tools registered.
func New(d Deps) *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "catprint",
		Version: "0.3.0",
	}, &mcpsdk.ServerOptions{
		Instructions: "Thermal printer. Call get_printer_capabilities before print_markdown. " +
			"Server validates markdown and adds timestamp header + tearline footer automatically.",
	})

	registerCapabilities(srv)
	registerStatus(srv, d)
	registerPrintMarkdown(srv, d)
	registerPrintImage(srv)
	return srv
}

// ---- get_printer_capabilities ----

type capsOut struct {
	Caps printer.Capabilities `json:"capabilities"`
}

func registerCapabilities(srv *mcpsdk.Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "get_printer_capabilities",
		Description: "Always call this before composing content for printing. " +
			"Returns paper dimensions, line length limits, supported markdown subset, " +
			"and formatting guidance. The server validates your markdown before printing " +
			"and will return actionable errors if constraints are violated.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, capsOut, error) {
		return nil, capsOut{Caps: printer.CurrentCapabilities()}, nil
	})
}

// ---- get_printer_status ----

type statusOut struct {
	Reachable        bool   `json:"reachable"`
	LastJobStatus    string `json:"last_job_status,omitempty"`
	QueueDepth       int    `json:"queue_depth"`
	JobsPendingRetry int    `json:"jobs_pending_retry"`
	PrinterAddr      string `json:"printer_addr,omitempty"`
	Note             string `json:"note"`
}

func registerStatus(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "get_printer_status",
		Description: "Returns connection reachability and last known state. " +
			"Note: 'sent' means bytes were transmitted over BLE, not that paper was ejected. " +
			"Paper-out is not reliably detectable on this printer model.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, statusOut, error) {
		queued, _ := d.Store.CountByStatus(jobs.StatusQueued)
		failed, _ := d.Store.CountByStatus(jobs.StatusFailed)

		last := ""
		if recent, _ := d.Store.List(1); len(recent) > 0 {
			last = string(recent[0].Status)
		}

		out := statusOut{
			Reachable:        d.Queue.Addr() != "",
			LastJobStatus:    last,
			QueueDepth:       queued,
			JobsPendingRetry: failed,
			PrinterAddr:      d.Queue.Addr(),
			Note:             "sent = BLE bytes transmitted; not a paper-confirmation",
		}
		return nil, out, nil
	})
}

// ---- print_markdown ----

type printMarkdownIn struct {
	Content string `json:"content" jsonschema:"the markdown content to print"`
}

type printMarkdownOut struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

type validationErrOut struct {
	Error      string               `json:"error"`
	Violations []validate.Violation `json:"violations"`
}

func registerPrintMarkdown(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "print_markdown",
		Description: "Print markdown to the thermal printer. " +
			"Content must conform to get_printer_capabilities constraints. " +
			"Server validates before rendering and returns line-specific errors on violation. " +
			"Do not include timestamp or tearline — server adds these automatically. " +
			"Returns job_id and final status (sent | failed | expired | queued).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in printMarkdownIn) (*mcpsdk.CallToolResult, printMarkdownOut, error) {
		if r := validate.Validate(in.Content); !r.OK() {
			body, _ := json.Marshal(validationErrOut{Error: r.Error, Violations: r.Violations})
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
			}, printMarkdownOut{}, nil
		}

		waitCtx, cancel := context.WithTimeout(ctx, PrintWaitTimeout)
		defer cancel()
		j, err := d.Queue.SubmitAndWait(waitCtx, "mcp", in.Content)
		if err != nil {
			return nil, printMarkdownOut{}, fmt.Errorf("submit: %w", err)
		}
		return nil, printMarkdownOut{JobID: j.ID, Status: string(j.Status)}, nil
	})
}

// ---- print_image (stub) ----

type printImageIn struct {
	Base64Png string `json:"base64_png"`
}

type printImageOut struct {
	Error string `json:"error"`
}

func registerPrintImage(srv *mcpsdk.Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "print_image",
		Description: "Print a base64-encoded PNG. Not yet implemented; returns an error.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ printImageIn) (*mcpsdk.CallToolResult, printImageOut, error) {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "image printing not yet implemented"}},
		}, printImageOut{Error: "image printing not yet implemented"}, nil
	})
}
