package overlay

import (
	"image"
	"image/draw"
	"testing"
)

func dimAll(badges []Badge) []Badge {
	out := make([]Badge, len(badges))
	for i, b := range badges {
		b.Dim, b.Typed = true, 0
		out[i] = b
	}
	return out
}

func planFor(t *testing.T, badges []Badge, viewport Size) Plan {
	t.Helper()
	plan, err := NewPlan(scene(badges, viewport))
	if err != nil {
		t.Fatalf("NewPlan() error: %v", err)
	}
	return plan
}

// composite stacks the backdrop and the badge layers the way Herdr does,
// each at the cells it declares.
func composite(t *testing.T, backdrop Frame, layers []Frame) *image.RGBA {
	t.Helper()
	out := image.NewRGBA(image.Rect(0, 0, backdrop.Width, backdrop.Height))
	draw.Draw(out, out.Bounds(), decode(t, backdrop), image.Point{}, draw.Src)
	for _, layer := range layers {
		at := image.Rect(
			layer.Col*cell.Width, layer.Row*cell.Height,
			layer.Col*cell.Width+layer.Width, layer.Row*cell.Height+layer.Height,
		)
		draw.Draw(out, at, decode(t, layer), image.Point{}, draw.Over)
	}
	return out
}

// TestLayersCompositeToTheFrame is what makes narrowing by layer safe to
// do at all: the dim backdrop with a bright layer over every badge that
// still matches has to be the same picture as re-encoding the whole
// viewport, pixel for pixel. It holds because every bright thing a badge
// layer draws is opaque, so it covers the ruled-out version underneath
// rather than blending with it.
//
// It holds on one precondition: no ruled-out badge carries typed progress,
// which the backdrop was drawn without and no bright layer would cover.
// marker.backdropShows enforces it by re-rendering the frame instead.
func TestLayersCompositeToTheFrame(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 40, Rows: 8}
	badges := []Badge{
		{Row: 1, Col: 6, Before: 6, Width: 12, Code: "as", Typed: 1},
		{Row: 3, Col: 20, Before: 4, Width: 9, Code: "ad", Dim: true},
		{Row: 5, Col: 2, Before: 2, Width: 14, Code: "af", Dim: true},
		{Row: 6, Col: 25, Before: 5, Width: 7, Code: "ag", Typed: 1},
	}
	plan := planFor(t, badges, viewport)

	backdrop, err := plan.Frame(dimAll(badges))
	if err != nil {
		t.Fatalf("backdrop: %v", err)
	}
	var layers []Frame
	for i := range plan.Placed() {
		if badges[plan.Link(i)].Dim {
			continue
		}
		layer, err := plan.Layer(i, badges)
		if err != nil {
			t.Fatalf("layer %d: %v", i, err)
		}
		layers = append(layers, layer)
	}
	if len(layers) != 2 {
		t.Fatalf("expected a layer per matching badge, got %d", len(layers))
	}

	frame, err := plan.Frame(badges)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	want := image.NewRGBA(image.Rect(0, 0, frame.Width, frame.Height))
	draw.Draw(want, want.Bounds(), decode(t, frame), image.Point{}, draw.Src)
	got := composite(t, backdrop, layers)

	for y := range want.Bounds().Dy() {
		for x := range want.Bounds().Dx() {
			if got.RGBAAt(x, y) != want.RGBAAt(x, y) {
				t.Fatalf("pixel %d,%d: layered %v, frame %v", x, y,
					got.RGBAAt(x, y), want.RGBAAt(x, y))
			}
		}
	}
}

// TestLayerCoversTheBadgeAndItsBox pins what a badge layer has to reach:
// both the hint and the link it boxes, however far place() moved them
// apart. A layer that covered only the hint would leave the box dim on a
// link that still matches.
func TestLayerCoversTheBadgeAndItsBox(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 40, Rows: 8}
	badges := []Badge{{Row: 4, Col: 20, Before: 0, Width: 10, Code: "as"}}
	plan := planFor(t, badges, viewport)
	if plan.Placed() != 1 {
		t.Fatalf("expected one placement, got %d", plan.Placed())
	}
	layer, err := plan.Layer(0, badges)
	if err != nil {
		t.Fatalf("layer: %v", err)
	}
	pl := plan.placed[0]
	if layer.Row > pl.row || layer.Row > pl.linkRow {
		t.Fatalf("layer starts at row %d, above badge row %d and link row %d",
			layer.Row, pl.row, pl.linkRow)
	}
	if layer.Row+layer.Rows <= max(pl.row, pl.linkRow) {
		t.Fatalf("layer ends at row %d, short of badge row %d and link row %d",
			layer.Row+layer.Rows, pl.row, pl.linkRow)
	}
	if layer.Col > min(pl.col, pl.linkCol) ||
		layer.Col+layer.Cols < max(pl.col+len(pl.code), pl.linkCol+pl.width) {
		t.Fatalf("layer spans cols %d..%d, badge %d..%d, link %d..%d",
			layer.Col, layer.Col+layer.Cols,
			pl.col, pl.col+len(pl.code), pl.linkCol, pl.linkCol+pl.width)
	}
	if layer.Width != layer.Cols*cell.Width || layer.Height != layer.Rows*cell.Height {
		t.Fatalf("layer is %dx%d px over %dx%d cells: the terminal upscales with a "+
			"linear filter, so it has to be drawn at the real cell size",
			layer.Width, layer.Height, layer.Cols, layer.Rows)
	}
}

// TestPlacementIgnoresNarrowing is the property the whole layered path
// rests on, and the one that would be silently wrong if it broke: typing
// must not move a badge. If placement depended on which links still
// matched, a code already on screen against one link would be redrawn
// against another, and the user would open a URL they were not looking at.
func TestPlacementIgnoresNarrowing(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 40, Rows: 8}
	badges := []Badge{
		{Row: 1, Col: 6, Before: 6, Width: 12, Code: "as"},
		{Row: 1, Col: 20, Before: 2, Width: 9, Code: "ad"},
		{Row: 2, Col: 4, Before: 4, Width: 14, Code: "af"},
	}
	base := planFor(t, badges, viewport)

	narrowed := []Badge{
		{Row: 1, Col: 6, Before: 6, Width: 12, Code: "as", Typed: 1},
		{Row: 1, Col: 20, Before: 2, Width: 9, Code: "ad", Dim: true},
		{Row: 2, Col: 4, Before: 4, Width: 14, Code: "af", Dim: true},
	}
	after := planFor(t, narrowed, viewport)

	if len(base.placed) != len(after.placed) {
		t.Fatalf("narrowing changed the placement count: %d then %d",
			len(base.placed), len(after.placed))
	}
	for i := range base.placed {
		was, now := base.placed[i], after.placed[i]
		if was.row != now.row || was.col != now.col || was.linkRow != now.linkRow ||
			was.linkCol != now.linkCol || was.width != now.width ||
			was.source != now.source || string(was.code) != string(now.code) {
			t.Fatalf("badge %d moved when the matches narrowed: %+v then %+v", i, was, now)
		}
	}
	if !base.SameLinks(narrowed) {
		t.Fatal("SameLinks should accept badges that differ only in Dim and Typed")
	}
}

// TestSameLinksRejectsADifferentScan catches the case a reused plan would
// get wrong: the links moved, so the codes have to be placed again.
func TestSameLinksRejectsADifferentScan(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 40, Rows: 8}
	badges := []Badge{{Row: 1, Col: 6, Before: 6, Width: 12, Code: "as"}}
	plan := planFor(t, badges, viewport)

	for name, other := range map[string][]Badge{
		"a link that scrolled":  {{Row: 2, Col: 6, Before: 6, Width: 12, Code: "as"}},
		"a link that moved":     {{Row: 1, Col: 7, Before: 6, Width: 12, Code: "as"}},
		"a link that resized":   {{Row: 1, Col: 6, Before: 6, Width: 13, Code: "as"}},
		"a different code":      {{Row: 1, Col: 6, Before: 6, Width: 12, Code: "ad"}},
		"a link that went away": nil,
		"a link that appeared": {
			{Row: 1, Col: 6, Before: 6, Width: 12, Code: "as"},
			{Row: 3, Col: 6, Before: 6, Width: 12, Code: "ad"},
		},
	} {
		if plan.SameLinks(other) {
			t.Fatalf("%s: SameLinks should have rejected it", name)
		}
	}
}

// TestLayerIsFarSmallerThanTheFrame is the reason the layered path exists.
func TestLayerIsFarSmallerThanTheFrame(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 204, Rows: 57}
	badges := []Badge{{Row: 20, Col: 40, Before: 4, Width: 20, Code: "as"}}
	plan := planFor(t, badges, viewport)
	frame, err := plan.Frame(badges)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	layer, err := plan.Layer(0, badges)
	if err != nil {
		t.Fatalf("layer: %v", err)
	}
	framePixels := frame.Width * frame.Height
	layerPixels := layer.Width * layer.Height
	if layerPixels*50 > framePixels {
		t.Fatalf("a badge layer is %d px against the frame's %d: not worth a second path",
			layerPixels, framePixels)
	}
}
