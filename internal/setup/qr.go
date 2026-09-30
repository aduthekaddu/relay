package setup

import (
	"strings"

	"rsc.io/qr"
)

// QR renders text as a terminal QR code using half-block characters (two
// modules per character cell) with a 2-module quiet zone. With color,
// explicit black-on-white colors make it scannable on dark and light
// terminals alike; without color, dark modules are drawn as blocks.
func QR(text string, color bool) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 2
	n := code.Size
	dark := func(x, y int) bool {
		if x < 0 || y < 0 || x >= n || y >= n {
			return false
		}
		return code.Black(x, y)
	}
	var b strings.Builder
	for y := -quiet; y < n+quiet; y += 2 {
		b.WriteString("  ")
		if color {
			b.WriteString("\x1b[30;107m") // black on bright white
		}
		for x := -quiet; x < n+quiet; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		if color {
			b.WriteString("\x1b[0m")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
