// PD01 BLE driver. Protocol constants and CRC8 table ported verbatim from
// github.com/rhnvrm/catprinter/pkg/printer/pd01.go (reverse-engineered from
// the vendor APK). Do not "tidy" the table or constants — they are exact.
package printer

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"github.com/synestry/catprint/printer/ble"
)

// PD01 protocol — header 0x51 0x78 followed by command byte.
const (
	pd01Header0      = 0x51
	pd01Header1      = 0x78
	pd01CmdGetState  = 0xA3
	pd01CmdSetQual   = 0xA4
	pd01CmdLattice   = 0xA6
	pd01CmdSetPaper  = 0xA1
	pd01CmdPrintRow  = 0xA2
	pd01CmdSetEnergy = 0xAF
	pd01CmdApplyE    = 0xBE
	pd01CmdFeedPaper = 0xBD
	pd01CmdGetInfo   = 0xA8
	pd01Terminator   = 0xFF
)

// CRC8 lookup table — exact, do not regenerate.
var pd01ChecksumTable = []byte{
	0x00, 0x07, 0x0e, 0x09, 0x1c, 0x1b, 0x12, 0x15, 0x38, 0x3f, 0x36, 0x31, 0x24, 0x23, 0x2a, 0x2d,
	0x70, 0x77, 0x7e, 0x79, 0x6c, 0x6b, 0x62, 0x65, 0x48, 0x4f, 0x46, 0x41, 0x54, 0x53, 0x5a, 0x5d,
	0xe0, 0xe7, 0xee, 0xe9, 0xfc, 0xfb, 0xf2, 0xf5, 0xd8, 0xdf, 0xd6, 0xd1, 0xc4, 0xc3, 0xca, 0xcd,
	0x90, 0x97, 0x9e, 0x99, 0x8c, 0x8b, 0x82, 0x85, 0xa8, 0xaf, 0xa6, 0xa1, 0xb4, 0xb3, 0xba, 0xbd,
	0xc7, 0xc0, 0xc9, 0xce, 0xdb, 0xdc, 0xd5, 0xd2, 0xff, 0xf8, 0xf1, 0xf6, 0xe3, 0xe4, 0xed, 0xea,
	0xb7, 0xb0, 0xb9, 0xbe, 0xab, 0xac, 0xa5, 0xa2, 0x8f, 0x88, 0x81, 0x86, 0x93, 0x94, 0x9d, 0x9a,
	0x27, 0x20, 0x29, 0x2e, 0x3b, 0x3c, 0x35, 0x32, 0x1f, 0x18, 0x11, 0x16, 0x03, 0x04, 0x0d, 0x0a,
	0x57, 0x50, 0x59, 0x5e, 0x4b, 0x4c, 0x45, 0x42, 0x6f, 0x68, 0x61, 0x66, 0x73, 0x74, 0x7d, 0x7a,
	0x89, 0x8e, 0x87, 0x80, 0x95, 0x92, 0x9b, 0x9c, 0xb1, 0xb6, 0xbf, 0xb8, 0xad, 0xaa, 0xa3, 0xa4,
	0xf9, 0xfe, 0xf7, 0xf0, 0xe5, 0xe2, 0xeb, 0xec, 0xc1, 0xc6, 0xcf, 0xc8, 0xdd, 0xda, 0xd3, 0xd4,
	0x69, 0x6e, 0x67, 0x60, 0x75, 0x72, 0x7b, 0x7c, 0x51, 0x56, 0x5f, 0x58, 0x4d, 0x4a, 0x43, 0x44,
	0x19, 0x1e, 0x17, 0x10, 0x05, 0x02, 0x0b, 0x0c, 0x21, 0x26, 0x2f, 0x28, 0x3d, 0x3a, 0x33, 0x34,
	0x4e, 0x49, 0x40, 0x47, 0x52, 0x55, 0x5c, 0x5b, 0x76, 0x71, 0x78, 0x7f, 0x6a, 0x6d, 0x64, 0x63,
	0x3e, 0x39, 0x30, 0x37, 0x22, 0x25, 0x2c, 0x2b, 0x06, 0x01, 0x08, 0x0f, 0x1a, 0x1d, 0x14, 0x13,
	0xae, 0xa9, 0xa0, 0xa7, 0xb2, 0xb5, 0xbc, 0xbb, 0x96, 0x91, 0x98, 0x9f, 0x8a, 0x8d, 0x84, 0x83,
	0xde, 0xd9, 0xd0, 0xd7, 0xc2, 0xc5, 0xcc, 0xcb, 0xe6, 0xe1, 0xe8, 0xef, 0xfa, 0xfd, 0xf4, 0xf3,
}

func pd01Checksum(data []byte, start, length int) byte {
	crc := byte(0)
	for i := start; i < start+length; i++ {
		crc = pd01ChecksumTable[(crc^data[i])&0xff]
	}
	return crc
}

// Pre-built static commands.
var (
	cmdGetDevState     = []byte{0x51, 0x78, 0xa3, 0x00, 0x01, 0x00, 0x00, 0x00, 0xff}
	cmdSetQuality200   = []byte{0x51, 0x78, 0xa4, 0x00, 0x01, 0x00, 0x32, 0x9e, 0xff}
	cmdLatticeStart    = []byte{0x51, 0x78, 0xa6, 0x00, 0x0b, 0x00, 0xaa, 0x55, 0x17, 0x38, 0x44, 0x5f, 0x5f, 0x5f, 0x44, 0x38, 0x2c, 0xa1, 0xff}
	cmdLatticeEnd      = []byte{0x51, 0x78, 0xa6, 0x00, 0x0b, 0x00, 0xaa, 0x55, 0x17, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x17, 0x11, 0xff}
	cmdSetPaperDefault = []byte{0x51, 0x78, 0xa1, 0x00, 0x02, 0x00, 0x30, 0x00, 0xf9, 0xff}
)

func cmdFeedPaper(lines byte) []byte {
	c := []byte{0x51, 0x78, 0xbd, 0x00, 0x01, 0x00, lines, 0x00, 0xff}
	c[7] = pd01Checksum(c, 6, 1)
	return c
}

func cmdSetEnergy(val uint16) []byte {
	c := []byte{0x51, 0x78, 0xaf, 0x00, 0x02, 0x00, byte(val >> 8), byte(val), 0x00, 0xff}
	c[8] = pd01Checksum(c, 6, 2)
	return c
}

func cmdApplyEnergy() []byte {
	c := []byte{0x51, 0x78, 0xbe, 0x00, 0x01, 0x00, 0x01, 0x00, 0xff}
	c[7] = pd01Checksum(c, 6, 1)
	return c
}

// cmdPrintRowBytes — encodedRow must be BytesPerLine (48) bytes, LSB first.
func cmdPrintRowBytes(encodedRow []byte) []byte {
	out := make([]byte, 0, 6+len(encodedRow)+2)
	out = append(out, 0x51, 0x78, 0xa2, 0x00, byte(len(encodedRow)), 0x00)
	out = append(out, encodedRow...)
	out = append(out, 0x00, 0xff)
	out[len(out)-2] = pd01Checksum(out, 6, len(encodedRow))
	return out
}

// Printer is the high-level driver.
type Printer struct {
	conn *ble.Connection
	addr string
}

// Discover scans BLE for ~10s looking for a printer whose advertised name
// contains "PD01". Returns its MAC address.
func Discover(ctx context.Context) (string, error) {
	deadline := 10 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d < deadline {
			deadline = d
		}
	}
	devs, err := ble.ScanForName(deadline, "PD01")
	if err != nil {
		return "", err
	}
	if len(devs) == 0 {
		return "", fmt.Errorf("no PD01 printer found within %s", deadline)
	}
	return strings.ToUpper(devs[0].Address.String()), nil
}

// Connect opens a BLE connection and runs printer init (state + 200dpi quality).
func Connect(ctx context.Context, addr string) (*Printer, error) {
	conn, err := ble.NewConnection()
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- conn.Connect(addr) }()
	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	p := &Printer{conn: conn, addr: addr}
	if err := p.initialize(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return p, nil
}

func (p *Printer) initialize() error {
	if _, err := p.conn.Write(cmdGetDevState); err != nil {
		return fmt.Errorf("get device state: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := p.conn.Write(cmdSetQuality200); err != nil {
		return fmt.Errorf("set quality: %w", err)
	}
	time.Sleep(50 * time.Millisecond)
	return nil
}

func (p *Printer) Close() error { return p.conn.Close() }

// PrintBitmap sends a 1-bit grayscale image. Width MUST equal PrintWidthPx.
// Pixels with Gray.Y < 128 are printed black.
func (p *Printer) PrintBitmap(ctx context.Context, img *image.Gray) error {
	b := img.Bounds()
	if b.Dx() != PrintWidthPx {
		return fmt.Errorf("image width must be %d px, got %d", PrintWidthPx, b.Dx())
	}

	rows := make([][]byte, 0, b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := make([]byte, BytesPerLine)
		for x := 0; x < PrintWidthPx; x++ {
			c := img.GrayAt(b.Min.X+x, y)
			if c.Y < 128 {
				row[x/8] |= 1 << (x % 8) // LSB-first per pd01 protocol
			}
		}
		rows = append(rows, row)
	}
	return p.sendRows(ctx, rows)
}

// sendRows handles the full envelope: lattice start → rows → feed/lattice end.
func (p *Printer) sendRows(ctx context.Context, rows [][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	buf := make([]byte, 0, 64+len(rows)*(6+BytesPerLine+2))
	buf = append(buf, cmdGetDevState...)
	buf = append(buf, cmdSetQuality200...)
	buf = append(buf, cmdSetEnergy(0xFFFF)...)
	buf = append(buf, cmdApplyEnergy()...)
	buf = append(buf, cmdLatticeStart...)
	for _, r := range rows {
		buf = append(buf, cmdPrintRowBytes(r)...)
	}
	buf = append(buf, cmdFeedPaper(1)...)
	buf = append(buf, cmdSetPaperDefault...)
	buf = append(buf, cmdLatticeEnd...)
	buf = append(buf, cmdGetDevState...)

	p.conn.SetMTU(ble.ImageMTU)
	_, err := p.conn.Write(buf)
	return err
}

// Ping sends a GetDevState command. Used as keep-alive to defeat idle sleep.
func (p *Printer) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.conn.SetMTU(ble.TextMTU)
	_, err := p.conn.Write(cmdGetDevState)
	return err
}

// Feed advances paper by N blank lines (max 255 per command).
func (p *Printer) Feed(ctx context.Context, lines int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for lines > 0 {
		chunk := lines
		if chunk > 255 {
			chunk = 255
		}
		p.conn.SetMTU(ble.TextMTU)
		if _, err := p.conn.Write(cmdFeedPaper(byte(chunk))); err != nil {
			return err
		}
		lines -= chunk
	}
	return nil
}

// SolidBlack returns a fully-black 384×height image — useful for smoke tests.
func SolidBlack(height int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, PrintWidthPx, height))
	for i := range img.Pix {
		img.Pix[i] = 0
	}
	_ = color.Black
	return img
}
