// Package theme reads the colours the terminal is painted in, so the
// overlay borrows the user's palette.
package theme

import (
	"image/color"
	"strconv"
	"strings"
)

// OSC report keys: 10 is the default foreground, 11 the background, 4;3
// the palette's yellow.
const (
	KeyForeground = "10"
	KeyBackground = "11"
	KeyAccent     = "4;3"
)

// Keys is every colour the overlay asks for.
var Keys = []string{KeyForeground, KeyBackground, KeyAccent}

// Query is the escape sequence that asks for one colour.
func Query(key string) string { return "\x1b]" + key + ";?\x1b\\" }

type Colors struct {
	Foreground color.RGBA
	Background color.RGBA
	Accent     color.RGBA
}

// Fallback is what a terminal that answers nothing gets.
func Fallback() Colors {
	return Colors{
		Foreground: Opaque(color.White),
		Background: Opaque(color.Black),
		Accent:     Opaque(color.White),
	}
}

func (c *Colors) Set(key string, rgb color.RGBA) {
	switch key {
	case KeyForeground:
		c.Foreground = rgb
	case KeyBackground:
		c.Background = rgb
	case KeyAccent:
		c.Accent = rgb
	}
}

// Parse reads one OSC colour report. Components come back as hex of any
// width, so rgb:1e/1e/2e and rgb:1e1e/1e1e/2e2e are the same colour.
func Parse(reply string) (key string, rgb color.RGBA, ok bool) {
	body, found := strings.CutPrefix(reply, "\x1b]")
	if !found {
		return "", color.RGBA{}, false
	}
	body = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(body, "\a"), "\x1b\\"), "\x1b")
	key, components, found := strings.Cut(body, "rgb:")
	if !found {
		return "", color.RGBA{}, false
	}
	parts := strings.Split(components, "/")
	if len(parts) != 3 {
		return "", color.RGBA{}, false
	}
	var channel [3]uint8
	for i, part := range parts {
		if channel[i], ok = component(part); !ok {
			return "", color.RGBA{}, false
		}
	}
	return strings.TrimSuffix(key, ";"), color.RGBA{R: channel[0], G: channel[1], B: channel[2], A: 0xFF}, true
}

func component(s string) (uint8, bool) {
	if s == "" || len(s) > 4 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	full := uint64(1)<<(4*len(s)) - 1
	return uint8((v*0xFF + full/2) / full), true
}

// Mix moves a toward b by the given fraction.
func Mix(a, b color.RGBA, toward float64) color.RGBA {
	return color.RGBA{
		R: blend(a.R, b.R, toward),
		G: blend(a.G, b.G, toward),
		B: blend(a.B, b.B, toward),
		A: blend(a.A, b.A, toward),
	}
}

func blend(a, b uint8, toward float64) uint8 {
	return uint8(float64(a)*(1-toward) + float64(b)*toward + 0.5)
}

// Fade returns c at the given alpha, premultiplied as image/color expects.
func Fade(c color.RGBA, alpha uint8) color.RGBA {
	scale := func(v uint8) uint8 { return uint8(uint32(v) * uint32(alpha) / 0xFF) }
	return color.RGBA{R: scale(c.R), G: scale(c.G), B: scale(c.B), A: alpha}
}

func Opaque(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xFF}
}
