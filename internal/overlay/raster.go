package overlay

import "image"

// linkStroke is the link box width in pixels.
const linkStroke = 2

// badgeStroke is the badge outline width.
const badgeStroke = 1

// smoothScale is where glyphs switch to antialiased.
const smoothScale = 3

// boxLink ties a badge to its link.
func boxLink(img *image.Paletted, p placement, cell Cell, dim bool) {
	outline(img, cellRect(p.linkRow, p.linkCol, p.width, 1, cell),
		badgeColor(dim, colorBorder, colorDimBorder), linkStroke)
}

func drawBadge(img *image.Paletted, p placement, cell Cell, dim bool, typed int) {
	box := cellRect(p.row, p.col, len(p.code), 1, cell)
	fill(img, box, badgeColor(dim, colorBackground, colorDimBackground))
	// Inverted prefix reads as typed.
	for i := 0; i < typed; i++ {
		fill(img, cellRect(p.row, p.col+i, 1, 1, cell),
			badgeColor(dim, colorText, colorDimText))
	}
	outline(img, box, badgeColor(dim, colorBorder, colorDimBorder), badgeStroke)
	for i, r := range p.code {
		fg := badgeColor(dim, colorText, colorDimText)
		inverted := i < typed
		if inverted {
			fg = badgeColor(dim, colorBackground, colorDimBackground)
		}
		drawGlyph(img, r, box.Min.X+i*cell.Width, box.Min.Y, cell, fg, dim, inverted)
	}
}

func badgeColor(dim bool, bright, muted uint8) uint8 {
	if dim {
		return muted
	}
	return bright
}

func cellRect(row, col, cols, rows int, cell Cell) image.Rectangle {
	return image.Rect(col*cell.Width, row*cell.Height, (col+cols)*cell.Width, (row+rows)*cell.Height)
}

func drawGlyph(img *image.Paletted, r rune, x, y int, cell Cell, fg uint8, dim, inverted bool) {
	bitmap, ok := glyph(r)
	if !ok {
		return
	}
	scale := glyphScale(cell)
	x += (cell.Width - glyphWidth*scale) / 2
	y += (cell.Height - glyphHeight*scale) / 2
	if scale < smoothScale {
		for row, bits := range bitmap {
			for col := range glyphWidth {
				if bits&byte(1<<(glyphWidth-1-col)) == 0 {
					continue
				}
				fill(img, image.Rect(x+col*scale, y+row*scale, x+(col+1)*scale, y+(row+1)*scale), fg)
			}
		}
		return
	}
	drawSmoothGlyph(img, bitmap, x, y, scale, fg, dim, inverted)
}

// drawSmoothGlyph draws an antialiased scaled glyph.
func drawSmoothGlyph(img *image.Paletted, bitmap [glyphHeight]byte, x, y, scale int, fg uint8, dim, inverted bool) {
	w, h := glyphWidth*scale, glyphHeight*scale
	for oy := 0; oy < h; oy++ {
		for ox := 0; ox < w; ox++ {
			px, py := x+ox, y+oy
			if px < img.Rect.Min.X || px >= img.Rect.Max.X || py < img.Rect.Min.Y || py >= img.Rect.Max.Y {
				continue
			}
			x0 := (float64(ox) + 0.5) / float64(scale)
			x1 := (float64(ox) + 1.5) / float64(scale)
			y0 := (float64(oy) + 0.5) / float64(scale)
			y1 := (float64(oy) + 1.5) / float64(scale)
			level := int(coverage(bitmap, x0, x1, y0, y1)*aaSteps + 0.5)
			if level <= 0 {
				continue
			}
			index := fg
			if level < aaSteps {
				index = aaIndex(dim, inverted, level)
			}
			img.Pix[(py-img.Rect.Min.Y)*img.Stride+(px-img.Rect.Min.X)] = index
		}
	}
}

// coverage is the bitmap fraction a rect covers.
func coverage(bitmap [glyphHeight]byte, x0, x1, y0, y1 float64) float64 {
	area := 0.0
	for jy := int(y0); float64(jy) < y1; jy++ {
		if jy < 0 || jy >= glyphHeight {
			continue
		}
		bits := bitmap[jy]
		loy := max(y0, float64(jy))
		hiy := min(y1, float64(jy+1))
		for jx := int(x0); float64(jx) < x1; jx++ {
			if jx < 0 || jx >= glyphWidth {
				continue
			}
			if bits&byte(1<<(glyphWidth-1-jx)) == 0 {
				continue
			}
			area += (min(x1, float64(jx+1)) - max(x0, float64(jx))) * (hiy - loy)
		}
	}
	return area / ((x1 - x0) * (y1 - y0))
}

// glyphScale fits the cell with padding, minimum 1.
func glyphScale(cell Cell) int {
	return max(1, min((cell.Width-2)/glyphWidth, (cell.Height-2)/glyphHeight))
}

func fill(img *image.Paletted, at image.Rectangle, index uint8) {
	at = at.Intersect(img.Rect)
	if at.Empty() {
		return
	}
	w := at.Dx()
	x0 := at.Min.X - img.Rect.Min.X
	off0 := (at.Min.Y-img.Rect.Min.Y)*img.Stride + x0
	first := img.Pix[off0 : off0+w]
	for i := range first {
		first[i] = index
	}
	for y := at.Min.Y + 1; y < at.Max.Y; y++ {
		off := (y-img.Rect.Min.Y)*img.Stride + x0
		copy(img.Pix[off:off+w], first)
	}
}

func outline(img *image.Paletted, at image.Rectangle, index uint8, weight int) {
	fill(img, image.Rect(at.Min.X, at.Min.Y, at.Max.X, at.Min.Y+weight), index)
	fill(img, image.Rect(at.Min.X, at.Max.Y-weight, at.Max.X, at.Max.Y), index)
	fill(img, image.Rect(at.Min.X, at.Min.Y, at.Min.X+weight, at.Max.Y), index)
	fill(img, image.Rect(at.Max.X-weight, at.Min.Y, at.Max.X, at.Max.Y), index)
}
