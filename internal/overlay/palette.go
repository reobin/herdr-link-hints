package overlay

import (
	"image/color"

	"github.com/reobin/herdr-link-hints/internal/theme"
)

const (
	// colorClear must stay first: untouched pixels show the pane through.
	colorClear uint8 = iota
	colorBorder
	colorBackground
	colorText
	colorDimBorder
	colorDimBackground
	colorDimText
)

// aaBase starts the glyph edge blends.
const aaBase = colorDimText + 1

// aaSteps is coverage levels per edge pixel.
const aaSteps = 4

const aaLevels = aaSteps - 1

const (
	aaBright uint8 = iota
	aaBrightInverted
	aaDim
	aaDimInverted
	aaPairs
)

// dimAlpha is how far ruled-out hints fade.
const dimAlpha = 0xB0

// newPalette derives badge colours from the terminal.
func newPalette(c theme.Colors) color.Palette {
	accent, _ := theme.BestAccent(c)
	background := accent
	text := c.Background
	dimBackground := theme.Fade(theme.Mix(accent, c.Background, 0.55), dimAlpha)
	dimText := theme.Fade(c.Background, dimAlpha)
	palette := make(color.Palette, aaBase+aaPairs*aaLevels)
	palette[colorClear] = color.RGBA{}
	palette[colorBorder] = theme.Mix(accent, c.Background, 0.45)
	palette[colorBackground] = background
	palette[colorText] = text
	palette[colorDimBorder] = theme.Fade(theme.Mix(accent, c.Background, 0.65), dimAlpha)
	palette[colorDimBackground] = dimBackground
	palette[colorDimText] = dimText
	ramps := []struct {
		pair   uint8
		fg, bg color.RGBA
	}{
		{aaBright, text, background},
		{aaBrightInverted, background, text},
		{aaDim, dimText, dimBackground},
		{aaDimInverted, dimBackground, dimText},
	}
	for _, ramp := range ramps {
		for level := 1; level < aaSteps; level++ {
			palette[aaBase+ramp.pair*aaLevels+uint8(level-1)] = theme.Mix(ramp.bg, ramp.fg, float64(level)/aaSteps)
		}
	}
	return palette
}

// aaIndex is the palette entry for an edge pixel.
func aaIndex(dim, inverted bool, level int) uint8 {
	var pair uint8
	switch {
	case dim && inverted:
		pair = aaDimInverted
	case dim:
		pair = aaDim
	case inverted:
		pair = aaBrightInverted
	default:
		pair = aaBright
	}
	return aaBase + pair*aaLevels + uint8(level-1)
}
