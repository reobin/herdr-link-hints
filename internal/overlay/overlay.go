// Package overlay draws hint badges into an image Herdr composites over a
// pane: its graphics API takes pixels, not text.
package overlay

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/reobin/herdr-link-hints/internal/theme"
)

// LayerID names the one graphics layer this plugin owns per pane.
const LayerID = "link-hints"

// maxFrameBytes is Herdr's cap on an inline pane.graphics.set frame.
const maxFrameBytes = 512 << 10

// underlineHeight is the pixels of a cell's bottom the link rule takes.
const underlineHeight = 2

const (
	colorHole uint8 = iota
	colorScrim
	colorBorder
	colorBackground
	colorText
	colorDimBorder
	colorDimBackground
	colorDimText
)

// scrimAlpha reads as dimming only because Herdr alpha-blends the layer.
const scrimAlpha = 0x66

// dimAlpha is how far a ruled-out hint fades.
const dimAlpha = 0xB0

// newPalette derives every colour from the ones the terminal reported.
func newPalette(c theme.Colors) color.Palette {
	palette := make(color.Palette, colorDimText+1)
	palette[colorHole] = color.RGBA{}
	palette[colorScrim] = theme.Fade(c.Background, scrimAlpha)
	palette[colorBorder] = theme.Mix(c.Accent, c.Background, 0.45)
	palette[colorBackground] = c.Accent
	palette[colorText] = c.Background
	palette[colorDimBorder] = theme.Fade(theme.Mix(c.Accent, c.Background, 0.65), dimAlpha)
	palette[colorDimBackground] = theme.Fade(theme.Mix(c.Accent, c.Background, 0.55), dimAlpha)
	palette[colorDimText] = theme.Fade(c.Background, dimAlpha)
	return palette
}

// Badge is one hint code and the link it marks. Before is the blank cells
// left of the link, Width the cells the link covers, Dim whether the typed
// prefix has ruled it out.
type Badge struct {
	Row    int
	Col    int
	Before int
	Width  int
	Code   string
	Dim    bool
}

// Cell is the pixel size of a terminal cell, from pane.graphics.info.
type Cell struct {
	Width  int
	Height int
}

// Size is a pane's viewport in cells.
type Size struct {
	Cols int
	Rows int
}

// Scene is everything one pane's overlay is drawn from.
type Scene struct {
	Badges   []Badge
	Colors   theme.Colors
	Cell     Cell
	Viewport Size
}

// Frame is an encoded overlay and the placement Herdr needs for it.
type Frame struct {
	PNG    []byte
	Width  int // pixels
	Height int // pixels
	Row    int // cells, the placement origin
	Col    int
	Rows   int // cells, the area the image covers
	Cols   int
}

// Render covers the whole viewport, badges or not: the scrim has to reach
// everywhere the links do not.
func Render(s Scene) (Frame, error) {
	if s.Cell.Width <= 0 || s.Cell.Height <= 0 {
		return Frame{}, fmt.Errorf("cell size %dx%d", s.Cell.Width, s.Cell.Height)
	}
	if s.Viewport.Cols <= 0 || s.Viewport.Rows <= 0 {
		return Frame{}, fmt.Errorf("viewport %dx%d", s.Viewport.Cols, s.Viewport.Rows)
	}
	placed := clip(s.Badges, s.Viewport)
	img := image.NewPaletted(
		image.Rect(0, 0, s.Viewport.Cols*s.Cell.Width, s.Viewport.Rows*s.Cell.Height),
		newPalette(s.Colors),
	)
	fill(img, img.Rect, colorScrim)
	// Ruled-out links stay readable: the screen must not rearrange while you
	// narrow.
	for _, p := range placed {
		fill(img, cellRect(p.linkRow, p.linkCol, p.width, 1, s.Cell), colorHole)
	}
	for _, p := range placed {
		underline(img, p, s.Cell)
	}
	for _, p := range placed {
		drawBadge(img, p, s.Cell)
	}

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
		Rows:   s.Viewport.Rows,
		Cols:   s.Viewport.Cols,
	}, nil
}

// checkSize guards the cap here rather than letting the RPC fail with less
// to go on.
func checkSize(n int) error {
	if n > maxFrameBytes {
		return fmt.Errorf("overlay frame is %d bytes, over the %d Herdr accepts", n, maxFrameBytes)
	}
	return nil
}

// placement is a badge resolved to cells: row and col are the hint, link*
// its link.
type placement struct {
	row     int
	col     int
	code    []rune
	linkRow int
	linkCol int
	width   int
	dim     bool
}

type point struct {
	row int
	col int
}

// clip places each badge and drops one the viewport cannot hold. A placed
// badge joins the cells the next one prefers to avoid, which thins
// collisions; place still covers a taken cell as a defensive last resort.
func clip(badges []Badge, viewport Size) []placement {
	taken := linkCells(badges, viewport)
	var out []placement
	for _, b := range badges {
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
			dim:     b.Dim,
		})
	}
	return out
}

// linkCells marks every cell a link covers, so a badge can be steered off
// a neighbour's link.
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

// place keeps a hint off every link, not just the one it marks. It walks
// outward from the link: the gutter beside it first, then the rows above
// and below out to two away, trying the aligned column before a few
// columns either side, and the link's own row last. Covering one is the
// defensive last resort, unreachable in a realistic pane: the walk finds
// a free cell long before it runs out.
func place(b Badge, width int, viewport Size, taken map[point]bool) (row, col int) {
	fallback := point{b.Row, fit(b.Col, width, viewport.Cols)}
	seen := map[point]bool{}
	if b.Before >= width {
		left := point{b.Row, b.Col - width}
		seen[left] = true
		if free(taken, left, width) {
			return left.row, left.col
		}
	}
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
	for _, colOff := range []int{0, -1, 1, -2, 2, -3, 3} {
		if s, ok := try(point{b.Row, fit(b.Col+colOff, width, viewport.Cols)}); ok {
			return s.row, s.col
		}
	}
	return fallback.row, fallback.col
}

// fit slides a badge left so its whole code stays inside the pane.
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

// underline ties a badge to its link however far apart they were placed.
func underline(img *image.Paletted, p placement, cell Cell) {
	rule := cellRect(p.linkRow, p.linkCol, p.width, 1, cell)
	rule.Min.Y = rule.Max.Y - underlineHeight
	fill(img, rule, badgeColor(p.dim, colorBorder, colorDimBorder))
}

func drawBadge(img *image.Paletted, p placement, cell Cell) {
	box := cellRect(p.row, p.col, len(p.code), 1, cell)
	fill(img, box, badgeColor(p.dim, colorBackground, colorDimBackground))
	outline(img, box, badgeColor(p.dim, colorBorder, colorDimBorder))
	for i, r := range p.code {
		drawGlyph(img, r, box.Min.X+i*cell.Width, box.Min.Y, cell, badgeColor(p.dim, colorText, colorDimText))
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

func drawGlyph(img *image.Paletted, r rune, x, y int, cell Cell, index uint8) {
	bitmap, ok := glyph(r)
	if !ok {
		return
	}
	scale := glyphScale(cell)
	x += (cell.Width - glyphWidth*scale) / 2
	y += (cell.Height - glyphHeight*scale) / 2
	for row, bits := range bitmap {
		for col := range glyphWidth {
			if bits&byte(1<<(glyphWidth-1-col)) == 0 {
				continue
			}
			fill(img, image.Rect(x+col*scale, y+row*scale, x+(col+1)*scale, y+(row+1)*scale), index)
		}
	}
}

// glyphScale leaves a pixel of padding where the cell allows it, never
// below scale 1.
func glyphScale(cell Cell) int {
	return max(1, min((cell.Width-2)/glyphWidth, (cell.Height-2)/glyphHeight))
}

func fill(img *image.Paletted, at image.Rectangle, index uint8) {
	at = at.Intersect(img.Rect)
	for y := at.Min.Y; y < at.Max.Y; y++ {
		for x := at.Min.X; x < at.Max.X; x++ {
			img.SetColorIndex(x, y, index)
		}
	}
}

func outline(img *image.Paletted, at image.Rectangle, index uint8) {
	fill(img, image.Rect(at.Min.X, at.Min.Y, at.Max.X, at.Min.Y+1), index)
	fill(img, image.Rect(at.Min.X, at.Max.Y-1, at.Max.X, at.Max.Y), index)
	fill(img, image.Rect(at.Min.X, at.Min.Y, at.Min.X+1, at.Max.Y), index)
	fill(img, image.Rect(at.Max.X-1, at.Min.Y, at.Max.X, at.Max.Y), index)
}
