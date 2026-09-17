// Package theme reads terminal colours for the overlay.
package theme

import (
	"image/color"
	"math"
	"strconv"
	"strings"
)

// OSC report keys; the badge uses whichever accent contrasts best.
const (
	KeyForeground = "10"
	KeyBackground = "11"
	KeyAccentRed  = "4;1"
	KeyAccent     = "4;3"
	KeyAccentBlue = "4;4"
)

// Keys is every colour the overlay asks for.
var Keys = []string{KeyForeground, KeyBackground, KeyAccentRed, KeyAccent, KeyAccentBlue}

// MinBadgeContrast is the readable floor.
const MinBadgeContrast = 3.0

// Query is the escape sequence that asks for one colour.
func Query(key string) string { return "\x1b]" + key + ";?\x1b\\" }

type Colors struct {
	Foreground color.RGBA
	Background color.RGBA
	AccentRed  color.RGBA
	Accent     color.RGBA
	AccentBlue color.RGBA
}

// Fallback is a terminal that answers nothing.
func Fallback() Colors {
	return Colors{
		Foreground: Opaque(color.White),
		Background: Opaque(color.Black),
		AccentRed:  Opaque(color.White),
		Accent:     Opaque(color.White),
		AccentBlue: Opaque(color.White),
	}
}

func (c *Colors) Set(key string, rgb color.RGBA) {
	switch key {
	case KeyForeground:
		c.Foreground = rgb
	case KeyBackground:
		c.Background = rgb
	case KeyAccentRed:
		c.AccentRed = rgb
	case KeyAccent:
		c.Accent = rgb
	case KeyAccentBlue:
		c.AccentBlue = rgb
	}
}

// BestAccent picks the accent contrasting most with the background.
func BestAccent(c Colors) (color.RGBA, float64) {
	best, bestScore := c.Accent, Contrast(c.Accent, c.Background)
	if score := Contrast(c.AccentRed, c.Background); score > bestScore {
		best, bestScore = c.AccentRed, score
	}
	if score := Contrast(c.AccentBlue, c.Background); score > bestScore {
		best, bestScore = c.AccentBlue, score
	}
	return best, bestScore
}

// Contrast is the WCAG ratio of two opaque colours.
func Contrast(a, b color.RGBA) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c color.RGBA) float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 0xFF
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// Parse reads one OSC colour report.
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

// Mix moves a toward b by a fraction.
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

// Fade returns c at an alpha, premultiplied.
func Fade(c color.RGBA, alpha uint8) color.RGBA {
	scale := func(v uint8) uint8 { return uint8(uint32(v) * uint32(alpha) / 0xFF) }
	return color.RGBA{R: scale(c.R), G: scale(c.G), B: scale(c.B), A: alpha}
}

func Opaque(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xFF}
}
