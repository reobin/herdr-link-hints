// Package overlay draws hint badges into an image Herdr composites over a
// pane: its graphics API takes pixels, not text.
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

// LayerID names the one graphics layer this plugin owns per pane.
const LayerID = "link-hints"

// maxFrameBytes is Herdr's cap on an inline pane.graphics.set frame.
const maxFrameBytes = 512 << 10

// linkStroke is the pixels the box around a link takes on each side.
const linkStroke = 2

// badgeStroke is the pixels the badge's own outline takes. Lighter than the
// link box so the two read as a pair rather than one slab where they meet.
const badgeStroke = 1

const (
	// colorClear has to stay first: it is the index an untouched image is
	// already filled with, and every pixel the overlay does not draw on has
	// to let the pane through.
	colorClear uint8 = iota
	colorBorder
	colorBackground
	colorText
	colorDimBorder
	colorDimBackground
	colorDimText
)

// aaBase is the first palette entry past the flat colours: the blends a
// smoothed glyph edge is quantized into.
const aaBase = colorDimText + 1

// aaSteps is the coverage levels per smoothed edge pixel: 0 the badge
// background, aaSteps the glyph foreground.
const aaSteps = 4

// aaLevels is the blends stored per foreground/background pair.
const aaLevels = aaSteps - 1

const (
	aaBright uint8 = iota
	aaBrightInverted
	aaDim
	aaDimInverted
	aaPairs
)

// smoothScale is the glyph scale where hard nearest-neighbour edges start
// to read next to antialiased terminal text. Below it glyphs draw exactly
// as before.
const smoothScale = 3

// dimAlpha is how far a ruled-out hint fades.
const dimAlpha = 0xB0

// newPalette derives every colour from the ones the terminal reported. The
// badge takes the palette entry with the most contrast against the
// background, so it survives light themes where yellow alone is a smudge.
// The tail holds the edge blends a smoothed glyph is quantized into, one
// ramp per foreground/background pair it can sit on.
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

// aaIndex is the palette entry for an edge pixel covering level/aaSteps of
// the foreground over the background.
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

// Badge is one hint code and the link it marks. Before is the blank cells
// left of the link, Width the cells the link covers, Dim whether the typed
// prefix has ruled it out, Typed how many leading code runes are already
// typed.
type Badge struct {
	Row    int
	Col    int
	Before int
	Width  int
	Code   string
	Dim    bool
	Typed  int
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

// Plan is every badge resolved to the cells it will be drawn in. The
// placement depends only on where the links are, never on which of them a
// typed prefix still matches, so the whole-viewport frame and the
// per-badge layers drawn from one plan agree on where every badge sits,
// and a code goes on meaning the same link while the user narrows.
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

// Placed is how many badges the plan found room for. A badge the viewport
// could not hold is not one of them.
func (p Plan) Placed() int { return len(p.placed) }

// Link is the index into the badge slice the i-th placed badge came from.
func (p Plan) Link(i int) int { return p.placed[i].source }

// SameLinks reports whether badges mark the same links, in the same order,
// as the ones this plan placed. Only Dim and Typed may differ. A plan
// reused against anything else would draw a code that has already been
// shown against one link on top of another, so a caller that gets false
// builds a new plan instead.
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

// Frame is the whole viewport in one image: clear everywhere the badges and
// their link boxes are not, so the pane reads exactly as it did before the
// hints went up.
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

// Layer draws one placed badge and its link box into the smallest image
// that covers both, transparent everywhere else, so redrawing a badge
// costs its own few thousand pixels rather than the viewport's twelve
// million. The image is at the pane's real cell size: Herdr scales a layer
// to the cells it declares and the terminal upscales with a linear filter,
// so anything smaller would smear the badge's edges across a cell.
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

// stateOf is how the badge a placement came from is drawn now. Badges the
// plan has outlived leave it matched and untyped rather than failing: the
// caller checks SameLinks when it matters.
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

// Render is the whole viewport in one frame, for a caller with no reason
// to keep the plan.
func Render(s Scene) (Frame, error) {
	plan, err := NewPlan(s)
	if err != nil {
		return Frame{}, err
	}
	return plan.Frame(s.Badges)
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
// its link, source the badge it came from. Whether the badge is dimmed or
// part-typed is not here on purpose - that changes on every keystroke and
// the placement must not.
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

// clip places each badge and drops one the viewport cannot hold. A placed
// badge joins the cells the next one prefers to avoid, which thins
// collisions; place still covers a taken cell as a defensive last resort.
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

// place keeps a hint off every link, not just the one it marks. The link's
// own row comes first, flush against the link and working outward: a badge
// against its link reads as belonging to it, and hiding a character of the
// text beside it costs less than floating a row away from the box. Only
// then the rows above and below, out to two away. Covering a link is the defensive last resort,
// unreachable in a realistic pane: the walk finds a free cell long before
// it runs out.
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

// besideLink is where a badge can sit on its link's own row, nearest first:
// flush against the link on either side, then a cell or two further out.
// Offsets are whole badge widths, so the badge lands beside the link rather
// than half over it.
func besideLink(b Badge, width int, viewport Size) []int {
	after := b.Col + max(b.Width, 1)
	cols := []int{b.Col - width, after, b.Col - width - 1, after + 1, b.Col - width - 2, after + 2}
	for i, col := range cols {
		cols[i] = fit(col, width, viewport.Cols)
	}
	return cols
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

// boxLink ties a badge to its link however far apart they were placed. A box
// rather than a rule beneath it: nothing dims the pane any more, and a link
// only a few cells wide gets too little underline to read as marked.
func boxLink(img *image.Paletted, p placement, cell Cell, dim bool) {
	outline(img, cellRect(p.linkRow, p.linkCol, p.width, 1, cell),
		badgeColor(dim, colorBorder, colorDimBorder), linkStroke)
}

func drawBadge(img *image.Paletted, p placement, cell Cell, dim bool, typed int) {
	box := cellRect(p.row, p.col, len(p.code), 1, cell)
	fill(img, box, badgeColor(dim, colorBackground, colorDimBackground))
	// The typed prefix reads as already entered: its cells are inverted
	// against the rest of the badge, reusing the badge's own colours so no
	// new palette entry is needed.
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

// drawSmoothGlyph covers each output pixel against the bitmap and writes
// the quantized blend of the foreground over the background, so a scaled-up
// glyph reads antialiased next to terminal text. The footprint carries a
// half-pixel phase: at integer scales every edge would otherwise land on a
// pixel boundary and no coverage could ever be fractional. At these sizes
// the cost is nothing next to the PNG encode.
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

// coverage is the fraction of the source-space rect the bitmap covers. The
// half-pixel phase keeps rect edges off integer source coordinates, so the
// overlap never needs an exact-boundary rule.
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

// glyphScale leaves a pixel of padding where the cell allows it, never
// below scale 1.
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
