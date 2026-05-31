// Vendored from github.com/rhnvrm/catprinter/pkg/bluetooth.
package ble

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"tinygo.org/x/bluetooth"
)

// muka/go-bluetooth (pulled in by tinygo's Linux/BlueZ backend) logs noisy
// "MapToStruct: invalid field" warnings on the global logrus logger whenever
// BlueZ exposes a property its structs don't map. They are harmless; raise the
// threshold so only real errors surface.
func init() {
	logrus.SetLevel(logrus.ErrorLevel)
}

const (
	ImageMTU          = 123
	TextMTU           = 83
	ReceiveBufferSize = 4096
	ConnectTimeout    = 30 * time.Second
)

var (
	ErrNotConnected    = errors.New("bluetooth: not connected")
	ErrWriteFailed     = errors.New("bluetooth: write failed")
	ErrReadFailed      = errors.New("bluetooth: read failed")
	ErrDeviceNotFound  = errors.New("bluetooth: device not found")
	ErrServiceNotFound = errors.New("bluetooth: service not found")
	ErrCharNotFound    = errors.New("bluetooth: characteristic not found")
)

type Connection struct {
	adapter *bluetooth.Adapter
	device  *bluetooth.Device

	writeChar  bluetooth.DeviceCharacteristic
	notifyChar bluetooth.DeviceCharacteristic

	readBuf   []byte
	readMutex sync.Mutex

	writeMutex sync.Mutex

	connected bool
	connMutex sync.RWMutex

	currentMTU int

	// EnableNotify subscribes to the printer's notify characteristic. Off by
	// default: tinygo fires the notification callback on an arbitrary WinRT
	// thread, which races readBuf and segfaults on Windows. Printing is
	// fire-and-forget (we never read the responses), so leave this off.
	EnableNotify bool
}

var (
	enableOnce sync.Once
	enableErr  error
)

// enableAdapter enables the default adapter exactly once. tinygo's WinRT
// backend cannot be re-enabled — a second Enable() after a failed connect
// returns "Incorrect function." and corrupts adapter state. So enable once
// and reuse for the process lifetime.
func enableAdapter() (*bluetooth.Adapter, error) {
	enableOnce.Do(func() {
		enableErr = bluetooth.DefaultAdapter.Enable()
	})
	if enableErr != nil {
		return nil, fmt.Errorf("failed to enable bluetooth adapter: %w", enableErr)
	}
	return bluetooth.DefaultAdapter, nil
}

func NewConnection() (*Connection, error) {
	adapter, err := enableAdapter()
	if err != nil {
		return nil, err
	}
	return &Connection{
		adapter:    adapter,
		readBuf:    make([]byte, 0, ReceiveBufferSize),
		currentMTU: TextMTU,
	}, nil
}

func (c *Connection) SetMTU(mtu int) { c.currentMTU = mtu }

func (c *Connection) IsConnected() bool {
	c.connMutex.RLock()
	defer c.connMutex.RUnlock()
	return c.connected
}

func (c *Connection) setConnected(state bool) {
	c.connMutex.Lock()
	defer c.connMutex.Unlock()
	c.connected = state
}

func (c *Connection) Connect(address string) error {
	if c.IsConnected() {
		return errors.New("already connected")
	}

	mac, err := bluetooth.ParseMAC(address)
	if err != nil {
		return fmt.Errorf("invalid bluetooth address: %w", err)
	}

	addr := bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}

	device, err := c.adapter.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("failed to connect to device: %w", err)
	}
	c.device = device

	services, err := device.DiscoverServices(nil)
	if err != nil {
		return fmt.Errorf("failed to discover services: %w", err)
	}
	if len(services) == 0 {
		return ErrServiceNotFound
	}

	const (
		targetServiceUUID = "0000ae30-0000-1000-8000-00805f9b34fb"
		writeCharUUID     = "0000ae01-0000-1000-8000-00805f9b34fb"
		notifyCharUUID    = "0000ae02-0000-1000-8000-00805f9b34fb"
	)

	var writeChar, notifyChar bluetooth.DeviceCharacteristic
	foundWrite, foundNotify := false, false

	for _, service := range services {
		if service.UUID().String() != targetServiceUUID {
			continue
		}
		chars, err := service.DiscoverCharacteristics(nil)
		if err != nil {
			continue
		}
		for _, char := range chars {
			u := char.UUID().String()
			switch u {
			case writeCharUUID:
				writeChar = char
				foundWrite = true
			case notifyCharUUID:
				notifyChar = char
				foundNotify = true
			}
		}
		break
	}

	if !foundWrite {
		return fmt.Errorf("%w: PD01 write characteristic (ae01) not found", ErrCharNotFound)
	}

	c.writeChar = writeChar
	c.notifyChar = notifyChar

	if foundNotify && c.EnableNotify {
		_ = c.notifyChar.EnableNotifications(func(buf []byte) {
			c.readMutex.Lock()
			defer c.readMutex.Unlock()
			c.readBuf = append(c.readBuf, buf...)
		})
	}

	c.setConnected(true)
	return nil
}

func (c *Connection) Disconnect() error {
	if !c.IsConnected() {
		return nil
	}
	var err error
	if c.device != nil {
		err = c.device.Disconnect()
	}
	c.setConnected(false)
	c.readBuf = c.readBuf[:0]
	return err
}

func (c *Connection) Write(p []byte) (n int, err error) {
	if !c.IsConnected() {
		return 0, ErrNotConnected
	}
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	for offset := 0; offset < len(p); offset += c.currentMTU {
		end := offset + c.currentMTU
		if end > len(p) {
			end = len(p)
		}
		chunk := p[offset:end]
		if _, werr := c.writeChar.WriteWithoutResponse(chunk); werr != nil {
			return n, fmt.Errorf("%w: %v", ErrWriteFailed, werr)
		}
		n += len(chunk)
		time.Sleep(10 * time.Millisecond)
	}
	return n, nil
}

func (c *Connection) Read(p []byte) (n int, err error) {
	if !c.IsConnected() {
		return 0, ErrNotConnected
	}
	c.readMutex.Lock()
	defer c.readMutex.Unlock()
	if len(c.readBuf) == 0 {
		return 0, io.EOF
	}
	n = copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *Connection) Close() error { return c.Disconnect() }
