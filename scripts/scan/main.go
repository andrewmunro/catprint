// Scan all BLE devices for 15s. Use to find printer's actual advertised name.
package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/synestry/catprint/printer/ble"
)

func main() {
	fmt.Println("scanning 15s...")
	devs, err := ble.Scan(15 * time.Second)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].RSSI > devs[j].RSSI })
	for _, d := range devs {
		name := d.Name
		if name == "" {
			name = "(no name)"
		}
		fmt.Printf("%4d dBm  %s  %q\n", d.RSSI, d.Address.String(), name)
	}
	fmt.Printf("total: %d\n", len(devs))
}
