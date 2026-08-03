// Vendored from github.com/rhnvrm/catprinter/pkg/bluetooth.
package ble

import (
	"errors"
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

// stopGrace is how long we wait for adapter.Scan to return after StopScan
// before declaring the adapter wedged.
const stopGrace = 10 * time.Second

// ErrScanWedged means a previous scan goroutine never returned after StopScan.
// Starting another scan would orphan it permanently, so we refuse instead.
var ErrScanWedged = errors.New("bluetooth: scan goroutine wedged, adapter unusable until restart")

var (
	// One scan at a time per adapter: tinygo returns errScanning immediately
	// for a concurrent call, which used to leave the earlier goroutine running
	// with nobody waiting on it.
	scanMu sync.Mutex
	wedged bool
)

func Scan(duration time.Duration) ([]Device, error) {
	if duration == 0 {
		duration = 10 * time.Second
	}
	adapter, err := enableAdapter()
	if err != nil {
		return nil, err
	}

	scanMu.Lock()
	defer scanMu.Unlock()
	if wedged {
		return nil, ErrScanWedged
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
	stopErr := adapter.StopScan()

	// Always wait for adapter.Scan to return, even when StopScan failed.
	// Returning early orphans that goroutine: its deferred bus.RemoveSignal
	// never runs, so godbus keeps spawning goroutines parked forever on the
	// unbuffered signal channel, and the scan's internal device map grows for
	// the life of the process. That path leaked ~27GB and OOM-killed the box.
	select {
	case err := <-scanErr:
		if stopErr != nil {
			return devices, fmt.Errorf("failed to stop scan: %w", stopErr)
		}
		if err != nil {
			return devices, fmt.Errorf("scan failed: %w", err)
		}
		return devices, nil
	case <-time.After(stopGrace):
		// Refuse all future scans rather than stack up more leaking goroutines.
		wedged = true
		return devices, ErrScanWedged
	}
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
