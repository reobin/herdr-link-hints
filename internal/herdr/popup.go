package herdr

import (
	"strconv"
	"strings"
)

// Herdr clamps a popup to these outer sizes.
const (
	popupMinWidth  = 6
	popupMinHeight = 4
)

// PopupRect is where Herdr centres a popup of the given outer size on the
// surface, mirroring its geometry: a size is cells, a percentage of the
// surface, or blank for half of it, clamped to the minimum and the
// surface. False when the popup cannot open at all.
func PopupRect(area Rect, width, height string) (Rect, bool) {
	if area.Width <= 0 || area.Height <= 0 {
		return Rect{}, false
	}
	w := min(max(popupSizeCells(width, area.Width), popupMinWidth), area.Width)
	h := min(max(popupSizeCells(height, area.Height), popupMinHeight), area.Height)
	if w < popupMinWidth || h < popupMinHeight {
		return Rect{}, false
	}
	return Rect{
		X:      area.X + (area.Width-w)/2,
		Y:      area.Y + (area.Height-h)/2,
		Width:  w,
		Height: h,
	}, true
}

// popupSizeCells resolves one size the way the server does; a value the
// server would refuse falls to its default, as OpenPane drops it.
func popupSizeCells(size string, available int) int {
	if percent, ok := strings.CutSuffix(size, "%"); ok {
		n, err := strconv.Atoi(percent)
		if err == nil && n >= 1 && n <= 100 {
			return available * n / 100
		}
		return available / 2
	}
	if n, err := strconv.Atoi(size); err == nil && n >= 0 {
		return n
	}
	return available / 2
}
