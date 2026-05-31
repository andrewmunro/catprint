// catprint server: SQLite job queue + BLE printer + MCP HTTP transport.
// Phase 3 — Claude Desktop can print via MCP tools.
package main

import (
	"context"
	"flag"
	"image"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/synestry/catprint/jobs"
	"github.com/synestry/catprint/mcp"
	"github.com/synestry/catprint/printer"
	"github.com/synestry/catprint/render"
	"github.com/synestry/catprint/voice"
	"github.com/synestry/catprint/web"
)

func main() {
	addr := flag.String("addr", getenv("PRINTER_ADDRESS", ""), "BLE MAC; if blank, discovered + cached")
	port := flag.String("port", getenv("PORT", "38827"), "HTTP listen port (serves web at / and MCP at /mcp)")
	dbPath := flag.String("db", getenv("DB_PATH", "jobs.db"), "SQLite path")
	addrCache := flag.String("addr-cache", ".printer_addr", "MAC cache file")
	keepalive := flag.Duration("keepalive", 20*time.Second, "BLE ping interval (0 disables, reconnect per job)")
	geminiKey := flag.String("gemini-key", getenv("GOOGLE_API_KEY", ""), "Gemini API key for /voice (empty disables voice)")
	flag.Parse()

	store, err := jobs.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	q, err := printer.NewQueue(printer.Config{
		Store:             store,
		Render:            renderForQueue,
		PrinterAddr:       *addr,
		AddrCachePath:     *addrCache,
		KeepAliveInterval: *keepalive,
	})
	if err != nil {
		log.Fatalf("queue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx)
	defer func() {
		cancel()
		q.Stop()
	}()

	mcpServer := mcp.New(mcp.Deps{Queue: q, Store: store})
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(_ *http.Request) *mcpsdk.Server {
		return mcpServer
	}, &mcpsdk.StreamableHTTPOptions{
		// cloudflared connects over loopback, so the SDK's DNS-rebinding guard
		// (loopback local addr + non-loopback Host) rejects tunnel requests
		// whose Host is the public hostname. We sit behind the tunnel, so turn
		// it off. NOTE: the endpoint is then reachable with no auth — add a
		// token/Access policy before relying on this being private.
		DisableLocalhostProtection: true,
	})

	// One mux, one port. The web UI owns "/" and its API paths; MCP owns
	// "/mcp". No path overlap, so a single Cloudflare hostname serves both.
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/voice", voice.Handler(voice.Deps{Queue: q, APIKey: *geminiKey}))
	mux.Handle("/", web.Handler(web.Deps{Queue: q, Store: store}))

	httpSrv := &http.Server{
		Addr:              ":" + *port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("catprint listening on :%s  (web=/  mcp=/mcp)", *port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func renderForQueue(content string) (*image.Gray, error) {
	return render.RenderContent(content, render.DefaultChrome("print"))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
