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

	"net/http/pprof"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/synestry/catprint/jobs"
	"github.com/synestry/catprint/mcp"
	"github.com/synestry/catprint/printer"
	"github.com/synestry/catprint/render"
	"github.com/synestry/catprint/web"
)

func main() {
	addr := flag.String("addr", getenv("PRINTER_ADDRESS", ""), "BLE MAC; if blank, discovered at runtime")
	port := flag.String("port", getenv("PORT", "38827"), "HTTP listen port (serves web at / and MCP at /mcp)")
	dbPath := flag.String("db", getenv("DB_PATH", "jobs.db"), "SQLite path")
	keepalive := flag.Duration("keepalive", 20*time.Second, "BLE ping interval (0 disables, reconnect per job)")
	pprofPort := flag.String("pprof-port", getenv("PPROF_PORT", "38828"), "loopback-only pprof port (blank disables)")
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

	// Diagnostics on loopback only — deliberately NOT on the mux above, which
	// cloudflared exposes publicly with no auth. Heap/goroutine dumps would
	// leak job content and let anyone force an allocation spike.
	// Reach it with: curl localhost:38828/debug/pprof/goroutine?debug=1
	if *pprofPort != "" {
		pprofMux := http.NewServeMux()
		pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
		pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		pprofSrv := &http.Server{
			Addr:              "127.0.0.1:" + *pprofPort,
			Handler:           pprofMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Printf("pprof listening on 127.0.0.1:%s", *pprofPort)
			if err := pprofSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("pprof: %v", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = pprofSrv.Shutdown(shutdownCtx)
		}()
	}

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
