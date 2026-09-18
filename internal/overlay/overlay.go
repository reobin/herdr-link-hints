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

// Rect is a cell rectangle in the viewport.
type Rect struct {
	Row  int
	Col  int
	Rows int
	Cols int
}

func (r Rect) Empty() bool { return r.Rows <= 0 || r.Cols <= 0 }

// Intersect is the cells both rects cover, empty when none.
func (r Rect) Intersect(o Rect) Rect {
	row, col := max(r.Row, o.Row), max(r.Col, o.Col)
	bottom := min(r.Row+r.Rows, o.Row+o.Rows)
	right := min(r.Col+r.Cols, o.Col+o.Cols)
	if bottom <= row || right <= col {
		return Rect{}
	}
	return Rect{Row: row, Col: col, Rows: bottom - row, Cols: right - col}
}

func (r Rect) overlaps(o Rect) bool { return !r.Intersect(o).Empty() }

// Scene is one pane's overlay inputs. Avoid is the cells a popup will
// cover: Herdr hides an image that touches a popup, so no badge lands
// there and frames tile around it.
type Scene struct {
	Badges   []Badge
	Colors   theme.Colors
	Cell     Cell
	Viewport Size
	Avoid    Rect
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

// Tile is one piece of a frame, named by the side of the avoided rect it
// sits on; a frame with nothing to avoid is one unnamed tile.
type Tile struct {
	Side  string
	Frame Frame
}

// Plan resolves badges to cells; placement ignores Dim and Typed.
type Plan struct {
	placed   []placement
	cell     Cell
	viewport Size
	avoid    Rect
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
	avoid := s.Avoid.Intersect(Rect{Rows: s.Viewport.Rows, Cols: s.Viewport.Cols})
	return Plan{
		placed:   clip(s.Badges, s.Viewport, avoid),
		cell:     s.Cell,
		viewport: s.Viewport,
		avoid:    avoid,
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
	whole := Rect{Rows: p.viewport.Rows, Cols: p.viewport.Cols}
	return p.encode(p.draw(whole, badges), whole)
}

// Tiles renders the viewport in pieces that keep clear of the avoided
// rect, skipping pieces with nothing drawn on them.
func (p Plan) Tiles(badges []Badge) ([]Tile, error) {
	var tiles []Tile
	for _, piece := range around(Rect{Rows: p.viewport.Rows, Cols: p.viewport.Cols}, p.avoid) {
		if !p.touches(piece.Rect) {
			continue
		}
		frame, err := p.encode(p.draw(piece.Rect, badges), piece.Rect)
		if err != nil {
			return nil, err
		}
		tiles = append(tiles, Tile{Side: piece.side, Frame: frame})
	}
	return tiles, nil
}

// Layer draws one badge and its link box, cut back to the side of the
// avoided rect the badge is on when the box runs under it.
func (p Plan) Layer(i int, badges []Badge) (Frame, error) {
	pl := p.placed[i]
	area := pl.bounds()
	if area.overlaps(p.avoid) {
		for _, piece := range around(area, p.avoid) {
			if piece.overlaps(pl.badgeRect()) {
				area = piece.Rect
				break
			}
		}
	}
	img := image.NewPaletted(cellRect(area.Row, area.Col, area.Cols, area.Rows, p.cell), p.palette)
	dim, typed := p.stateOf(badges, pl)
	boxLink(img, pl, p.cell, dim)
	drawBadge(img, pl, p.cell, dim, typed)
	return p.encode(img, area)
}

// draw renders every placed badge into an image covering area; drawing
// clips to the image, so cells outside stay out.
func (p Plan) draw(area Rect, badges []Badge) *image.Paletted {
	img := image.NewPaletted(cellRect(area.Row, area.Col, area.Cols, area.Rows, p.cell), p.palette)
	for _, pl := range p.placed {
		dim, _ := p.stateOf(badges, pl)
		boxLink(img, pl, p.cell, dim)
	}
	for _, pl := range p.placed {
		dim, typed := p.stateOf(badges, pl)
		drawBadge(img, pl, p.cell, dim, typed)
	}
	return img
}

// touches reports whether any badge or link box reaches into area.
func (p Plan) touches(area Rect) bool {
	for _, pl := range p.placed {
		if pl.badgeRect().overlaps(area) || pl.linkRect().overlaps(area) {
			return true
		}
	}
	return false
}

// stateOf is how a placed badge is drawn now.
func (p Plan) stateOf(badges []Badge, pl placement) (dim bool, typed int) {
	if pl.source < 0 || pl.source >= len(badges) {
		return false, 0
	}
	b := badges[pl.source]
	return b.Dim, min(max(b.Typed, 0), len(pl.code))
}

func (p Plan) encode(img *image.Paletted, area Rect) (Frame, error) {
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
		Row:    area.Row,
		Col:    area.Col,
		Rows:   area.Rows,
		Cols:   area.Cols,
	}, nil
}

// piece is one side of a rect once a hole is cut out of it.
type piece struct {
	side string
	Rect
}

// around splits bounds into the bands above, below, left and right of
// hole, dropping empty ones. Without a hole it is bounds alone, unnamed.
func around(bounds, hole Rect) []piece {
	hole = hole.Intersect(bounds)
	if hole.Empty() {
		return []piece{{"", bounds}}
	}
	bottom, right := hole.Row+hole.Rows, hole.Col+hole.Cols
	candidates := []piece{
		{"top", Rect{Row: bounds.Row, Col: bounds.Col, Rows: hole.Row - bounds.Row, Cols: bounds.Cols}},
		{"bottom", Rect{Row: bottom, Col: bounds.Col, Rows: bounds.Row + bounds.Rows - bottom, Cols: bounds.Cols}},
		{"left", Rect{Row: hole.Row, Col: bounds.Col, Rows: hole.Rows, Cols: hole.Col - bounds.Col}},
		{"right", Rect{Row: hole.Row, Col: right, Rows: hole.Rows, Cols: bounds.Col + bounds.Cols - right}},
	}
	pieces := make([]piece, 0, len(candidates))
	for _, c := range candidates {
		if !c.Empty() {
			pieces = append(pieces, c)
		}
	}
	return pieces
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
