// Holds a BLE connection open and pings the printer's GetDevState command
// every N seconds to defeat its idle sleep timer. Ctrl-C to stop.
//
// Usage:
//
//	keepalive.exe -addr AA:BB:CC:DD:EE:FF
//	keepalive.exe -addr ... -interval 20s
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/synestry/catprint/printer/ble"
)

// pd01CmdGetDevState — copied from printer/pd01.go so this tool doesn't pull
// the full driver. Same exact bytes.
var pd01CmdGetDevState = []byte{0x51, 0x78, 0xa3, 0x00, 0x01, 0x00, 0x00, 0x00, 0xff}

func main() {
	addr := flag.String("addr", "", "BLE MAC; if blank, read .printer_addr")
	interval := flag.Duration("interval", 20*time.Second, "ping interval")
	flag.Parse()

	if *addr == "" {
		b, _ := os.ReadFile(".printer_addr")
		*addr = strings.TrimSpace(string(b))
	}
	if *addr == "" {
		fmt.Fprintln(os.Stderr, "no MAC; use -addr or create .printer_addr")
		os.Exit(1)
	}

	conn, err := ble.NewConnection()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ble: %v\n", err)
		os.Exit(1)
	}
	log.Printf("connecting %s...", *addr)
	if err := conn.Connect(*addr); err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	log.Printf("connected; pinging every %s", *interval)

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("stopping...")
		cancel()
	}()

	t := time.NewTicker(*interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := conn.Write(pd01CmdGetDevState); err != nil {
				fmt.Fprintf(os.Stderr, "ping failed: %v — exiting\n", err)
				return
			}
			log.Println("ping ok")
		}
	}
}
