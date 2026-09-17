// Package overlay draws hint badges Herdr composites over a pane.
package overlay

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"slices"

	"github.com/reobin/herdr-link-hints/internal/theme"
)

// LayerID is this plugin's graphics layer per pane.
const LayerID = "link-hints"

// maxFrameBytes is Herdr's inline frame cap.
const maxFrameBytes = 512 << 10

// linkStroke is the link box width in pixels.
const linkStroke = 2

// badgeStroke is the badge outline width.
const badgeStroke = 1

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

// smoothScale is where glyphs switch to antialiased.
const smoothScale = 3

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

// Badge is one hint and its link.
type Badge struct {
	Row    int
	Col    int
	Before int
	Width  int
	Code   string
	Dim    bool
	Typed  int
}

// Cell is a terminal cell in pixels.
type Cell struct {
	Width  int
	Height int
}

// Size is a viewport in cells.
type Size struct {
	Cols int
	Rows int
}

// Scene is one pane's overlay inputs.
type Scene struct {
	Badges   []Badge
	Colors   theme.Colors
	Cell     Cell
	Viewport Size
}

// Frame is an encoded overlay plus placement.
type Frame struct {
	PNG    []byte
	Width  int // pixels
	Height int // pixels
	Row    int // cells, the placement origin
	Col    int
	Rows   int // cells, the area the image covers
	Cols   int
}

// Plan resolves badges to cells; placement ignores Dim and Typed.
type Plan struct {
	placed   []placement
	cell     Cell
	viewport Size
	palette  color.Palette
	links    []Badge
}

func NewPlan(s Scene) (Plan, error) {
	if s.Cell.Width <= 0 || s.Cell.Height <= 0 {
		return Plan{}, fmt.Errorf("cell size %dx%d", s.Cell.Width, s.Cell.Height)
	}
	if s.Viewport.Cols <= 0 || s.Viewport.Rows <= 0 {
		return Plan{}, fmt.Errorf("viewport %dx%d", s.Viewport.Cols, s.Viewport.Rows)
	}
	return Plan{
		placed:   clip(s.Badges, s.Viewport),
		cell:     s.Cell,
		viewport: s.Viewport,
		palette:  newPalette(s.Colors),
		links:    slices.Clone(s.Badges),
	}, nil
}

func (p Plan) Placed() int { return len(p.placed) }

func (p Plan) Link(i int) int { return p.placed[i].source }

// SameLinks reports whether badges mark the same links.
func (p Plan) SameLinks(badges []Badge) bool {
	if len(badges) != len(p.links) {
		return false
	}
	for i, b := range badges {
		was := p.links[i]
		if was.Row != b.Row || was.Col != b.Col || was.Before != b.Before ||
			was.Width != b.Width || was.Code != b.Code {
			return false
		}
	}
	return true
}

// Frame renders the whole viewport.
func (p Plan) Frame(badges []Badge) (Frame, error) {
	img := image.NewPaletted(
		image.Rect(0, 0, p.viewport.Cols*p.cell.Width, p.viewport.Rows*p.cell.Height),
		p.palette,
	)
	for _, pl := range p.placed {
		dim, _ := p.stateOf(badges, pl)
		boxLink(img, pl, p.cell, dim)
	}
	for _, pl := range p.placed {
		dim, typed := p.stateOf(badges, pl)
		drawBadge(img, pl, p.cell, dim, typed)
	}
	return p.encode(img, 0, 0, p.viewport.Rows, p.viewport.Cols)
}

// Layer draws one badge and its link box.
func (p Plan) Layer(i int, badges []Badge) (Frame, error) {
	pl := p.placed[i]
	row := min(pl.row, pl.linkRow)
	col := min(pl.col, pl.linkCol)
	rows := max(pl.row, pl.linkRow) + 1 - row
	cols := max(pl.col+len(pl.code), pl.linkCol+pl.width) - col
	img := image.NewPaletted(cellRect(row, col, cols, rows, p.cell), p.palette)
	dim, typed := p.stateOf(badges, pl)
	boxLink(img, pl, p.cell, dim)
	drawBadge(img, pl, p.cell, dim, typed)
	return p.encode(img, row, col, rows, cols)
}

// stateOf is how a placed badge is drawn now.
func (p Plan) stateOf(badges []Badge, pl placement) (dim bool, typed int) {
	if pl.source < 0 || pl.source >= len(badges) {
		return false, 0
	}
	b := badges[pl.source]
	return b.Dim, min(max(b.Typed, 0), len(pl.code))
}

func (p Plan) encode(img *image.Paletted, row, col, rows, cols int) (Frame, error) {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, img); err != nil {
		return Frame{}, fmt.Errorf("encode overlay: %w", err)
	}
	if err := checkSize(buf.Len()); err != nil {
		return Frame{}, err
	}
	return Frame{
		PNG:    buf.Bytes(),
		Width:  img.Rect.Dx(),
		Height: img.Rect.Dy(),
		Row:    row,
		Col:    col,
		Rows:   rows,
		Cols:   cols,
	}, nil
}

// Render draws the whole viewport without keeping the plan.
func Render(s Scene) (Frame, error) {
	plan, err := NewPlan(s)
	if err != nil {
		return Frame{}, err
	}
	return plan.Frame(s.Badges)
}

// checkSize guards Herdr's frame cap.
func checkSize(n int) error {
	if n > maxFrameBytes {
		return fmt.Errorf("overlay frame is %d bytes, over the %d Herdr accepts", n, maxFrameBytes)
	}
	return nil
}

// placement is a badge resolved to cells.
type placement struct {
	row     int
	col     int
	code    []rune
	linkRow int
	linkCol int
	width   int
	source  int
}

type point struct {
	row int
	col int
}

// clip places badges, dropping ones the viewport cannot hold.
func clip(badges []Badge, viewport Size) []placement {
	taken := linkCells(badges, viewport)
	var out []placement
	for i, b := range badges {
		if b.Row < 0 || b.Row >= viewport.Rows || b.Col < 0 || b.Col >= viewport.Cols {
			continue
		}
		code := []rune(b.Code)
		if len(code) == 0 {
			continue
		}
		row, col := place(b, len(code), viewport, taken)
		// A code cut short is the wrong thing to type.
		if col < 0 || col+len(code) > viewport.Cols {
			continue
		}
		occupy(taken, point{row, col}, len(code))
		out = append(out, placement{
			row:     row,
			col:     col,
			code:    code,
			linkRow: b.Row,
			linkCol: b.Col,
			width:   min(max(b.Width, 1), viewport.Cols-b.Col),
			source:  i,
		})
	}
	return out
}

// linkCells marks cells links cover.
func linkCells(badges []Badge, viewport Size) map[point]bool {
	taken := map[point]bool{}
	for _, b := range badges {
		if b.Row < 0 || b.Row >= viewport.Rows || b.Col < 0 || b.Col >= viewport.Cols {
			continue
		}
		for col := b.Col; col < min(b.Col+max(b.Width, 1), viewport.Cols); col++ {
			taken[point{b.Row, col}] = true
		}
	}
	return taken
}

// place keeps a hint off every link, covering one as a last resort.
func place(b Badge, width int, viewport Size, taken map[point]bool) (row, col int) {
	fallback := point{b.Row, fit(b.Col, width, viewport.Cols)}
	seen := map[point]bool{}
	try := func(s point) (point, bool) {
		if seen[s] {
			return point{}, false
		}
		seen[s] = true
		if free(taken, s, width) {
			return s, true
		}
		return point{}, false
	}
	for _, col := range besideLink(b, width, viewport) {
		if s, ok := try(point{b.Row, col}); ok {
			return s.row, s.col
		}
	}
	for _, colOff := range []int{0, -1, 1, -2, 2, -3, 3} {
		for _, rowOff := range []int{-1, 1, -2, 2} {
			r := b.Row + rowOff
			if r < 0 || r >= viewport.Rows {
				continue
			}
			if s, ok := try(point{r, fit(b.Col+colOff, width, viewport.Cols)}); ok {
				return s.row, s.col
			}
		}
	}
	return fallback.row, fallback.col
}

// besideLink is badge slots on the link's row, nearest first.
func besideLink(b Badge, width int, viewport Size) []int {
	after := b.Col + max(b.Width, 1)
	cols := []int{b.Col - width, after, b.Col - width - 1, after + 1, b.Col - width - 2, after + 2}
	for i, col := range cols {
		cols[i] = fit(col, width, viewport.Cols)
	}
	return cols
}

// fit slides a badge inside the pane.
func fit(col, width, cols int) int {
	return max(0, min(col, cols-width))
}

func occupy(taken map[point]bool, at point, width int) {
	for col := at.col; col < at.col+width; col++ {
		taken[point{at.row, col}] = true
	}
}

func free(taken map[point]bool, at point, width int) bool {
	for col := at.col; col < at.col+width; col++ {
		if taken[point{at.row, col}] {
			return false
		}
	}
	return true
}

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
