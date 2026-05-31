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
	"github.com/synestry/catprint/web"
)

func main() {
	addr := flag.String("addr", getenv("PRINTER_ADDRESS", ""), "BLE MAC; if blank, discovered + cached")
	mcpPort := flag.String("mcp-port", getenv("MCP_PORT", "9000"), "MCP HTTP listen port")
	webPort := flag.String("web-port", getenv("WEB_PORT", "8080"), "web UI listen port")
	dbPath := flag.String("db", getenv("DB_PATH", "jobs.db"), "SQLite path")
	addrCache := flag.String("addr-cache", ".printer_addr", "MAC cache file")
	keepalive := flag.Duration("keepalive", 20*time.Second, "BLE ping interval (0 disables, reconnect per job)")
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

	srv := mcp.New(mcp.Deps{Queue: q, Store: store})
	handler := mcpsdk.NewStreamableHTTPHandler(func(_ *http.Request) *mcpsdk.Server {
		return srv
	}, nil)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mcpSrv := &http.Server{
		Addr:              ":" + *mcpPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("MCP server listening on :%s/mcp", *mcpPort)
		if err := mcpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("mcp http: %v", err)
		}
	}()

	webSrv := &http.Server{
		Addr:              ":" + *webPort,
		Handler:           web.Handler(web.Deps{Queue: q, Store: store}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("web UI listening on :%s", *webPort)
		if err := webSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("web http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = mcpSrv.Shutdown(shutdownCtx)
	_ = webSrv.Shutdown(shutdownCtx)
}

func renderForQueue(content string) (*image.Gray, error) {
	return render.RenderMarkdown(content, render.DefaultChrome("mcp"))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
