// Phase 1 smoke test: discover printer, connect, print a solid black
// 384×60 rectangle, feed 4 lines, disconnect. If the rectangle comes out
// black and rectangular, the BLE protocol port is correct.
//
// Usage:
//
//	go run ./scripts/test_print                # discover by name
//	go run ./scripts/test_print AA:BB:CC:...   # skip discovery
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/synestry/catprint/printer"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var addr string
	if len(os.Args) >= 2 {
		addr = os.Args[1]
	} else {
		log.Println("scanning for PD01...")
		dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
		defer dcancel()
		a, err := printer.Discover(dctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discover failed: %v\n", err)
			os.Exit(1)
		}
		addr = a
		log.Printf("found printer at %s", addr)
	}

	log.Printf("connecting to %s...", addr)
	p, err := printer.Connect(ctx, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		os.Exit(1)
	}
	defer p.Close()

	log.Println("printing 384×60 solid black rectangle...")
	if err := p.PrintBitmap(ctx, printer.SolidBlack(60)); err != nil {
		fmt.Fprintf(os.Stderr, "print failed: %v\n", err)
		os.Exit(1)
	}

	log.Println("feeding 4 lines...")
	if err := p.Feed(ctx, 4); err != nil {
		fmt.Fprintf(os.Stderr, "feed failed: %v\n", err)
		os.Exit(1)
	}

	log.Println("done. Check the paper.")
}
