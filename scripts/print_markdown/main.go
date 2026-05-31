// Phase 2 paper test: validate + render markdown, then print it on real paper.
//
// Usage:
//
//	echo "# Hi\n- [ ] one" | go run ./scripts/print_markdown
//	go run ./scripts/print_markdown path/to/file.md
//	go run ./scripts/print_markdown -addr AA:BB:CC:DD:EE:FF path/to/file.md
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/synestry/catprint/printer"
	"github.com/synestry/catprint/render"
	"github.com/synestry/catprint/validate"
)

func main() {
	addr := flag.String("addr", "", "BLE MAC; if blank, read .printer_addr or scan")
	source := flag.String("source", "cli", "source tag for chrome header")
	flag.Parse()

	md, err := readMarkdown(flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if r := validate.Validate(md); !r.OK() {
		fmt.Fprintln(os.Stderr, "validation failed:")
		for _, v := range r.Violations {
			fmt.Fprintf(os.Stderr, "  line %d: %s (%q)\n", v.Line, v.Issue, v.Content)
		}
		os.Exit(2)
	}

	img, err := render.RenderMarkdown(md, render.DefaultChrome(*source))
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: %v\n", err)
		os.Exit(1)
	}
	log.Printf("rendered %dx%d", img.Bounds().Dx(), img.Bounds().Dy())

	if *addr == "" {
		*addr = readCachedAddr()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if *addr == "" {
		log.Println("scanning for PD01...")
		dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
		defer dcancel()
		a, err := printer.Discover(dctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discover: %v\n", err)
			os.Exit(1)
		}
		*addr = a
		log.Printf("found %s", a)
	}

	log.Printf("connecting %s...", *addr)
	p, err := printer.Connect(ctx, *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer p.Close()

	log.Println("printing...")
	if err := p.PrintBitmap(ctx, img); err != nil {
		fmt.Fprintf(os.Stderr, "print: %v\n", err)
		os.Exit(1)
	}
	if err := p.Feed(ctx, 4); err != nil {
		fmt.Fprintf(os.Stderr, "feed: %v\n", err)
		os.Exit(1)
	}
	log.Println("done")
}

func readMarkdown(args []string) (string, error) {
	if len(args) == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		return "", fmt.Errorf("read %s: %w", args[0], err)
	}
	return string(b), nil
}

func readCachedAddr() string {
	b, err := os.ReadFile(".printer_addr")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
