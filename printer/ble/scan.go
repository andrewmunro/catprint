// Vendored from github.com/rhnvrm/catprinter/pkg/bluetooth.
package ble

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

type Device struct {
	Address bluetooth.Address
	Name    string
	RSSI    int16
}

func Scan(duration time.Duration) ([]Device, error) {
	if duration == 0 {
		duration = 10 * time.Second
	}
	adapter, err := enableAdapter()
	if err != nil {
		return nil, err
	}

	devices := make([]Device, 0)
	seen := make(map[string]bool)

	var mu sync.Mutex

	// adapter.Scan blocks until StopScan is called. Run it in a goroutine and
	// stop it from the timer.
	scanErr := make(chan error, 1)
	go func() {
		scanErr <- adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
			addr := r.Address.String()
			mu.Lock()
			defer mu.Unlock()
			if seen[addr] {
				return
			}
			seen[addr] = true
			devices = append(devices, Device{Address: r.Address, Name: r.LocalName(), RSSI: r.RSSI})
		})
	}()

	time.Sleep(duration)
	if err := adapter.StopScan(); err != nil {
		return devices, fmt.Errorf("failed to stop scan: %w", err)
	}
	if err := <-scanErr; err != nil {
		return devices, fmt.Errorf("scan failed: %w", err)
	}
	return devices, nil
}

// ScanForName returns devices whose advertised name contains the substring (case-insensitive).
func ScanForName(duration time.Duration, namePart string) ([]Device, error) {
	all, err := Scan(duration)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(namePart)
	out := make([]Device, 0)
	for _, d := range all {
		if strings.Contains(strings.ToLower(d.Name), needle) {
			out = append(out, d)
		}
	}
	return out, nil
}
