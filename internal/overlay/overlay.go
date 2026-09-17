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
