package overlay

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// The popup footprint these tests cut out of a 40x8 viewport.
var hole = Rect{Row: 2, Col: 14, Rows: 3, Cols: 12}

func avoiding(badges []Badge, viewport Size, avoid Rect) Scene {
	s := scene(badges, viewport)
	s.Avoid = avoid
	return s
}

// Badges on every side of the hole, none under it.
func surrounding() []Badge {
	return []Badge{
		{Row: 0, Col: 6, Before: 6, Width: 8, Code: "as"},
		{Row: 7, Col: 20, Before: 4, Width: 6, Code: "ad"},
		{Row: 3, Col: 2, Before: 2, Width: 6, Code: "af"},
		{Row: 3, Col: 30, Before: 5, Width: 6, Code: "ag"},
	}
}

// Herdr hides an image whole when it touches the popup, so every tile has
// to keep off the avoided cells, and together they must show everything a
// frame would have shown outside them.
func TestTilesKeepClearOfTheAvoidedRect(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 40, Rows: 8}
	badges := surrounding()
	plan, err := NewPlan(avoiding(badges, viewport, hole))
	if err != nil {
		t.Fatal(err)
	}
	tiles, err := plan.Tiles(badges)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiles) != 4 {
		t.Fatalf("got %d tiles, want one per side: %+v", len(tiles), sides(tiles))
	}
	whole := Rect{Rows: viewport.Rows, Cols: viewport.Cols}
	got := image.NewRGBA(image.Rect(0, 0, whole.Cols*cell.Width, whole.Rows*cell.Height))
	for _, tile := range tiles {
		f := tile.Frame
		at := Rect{Row: f.Row, Col: f.Col, Rows: f.Rows, Cols: f.Cols}
		if at.overlaps(hole) {
			t.Fatalf("tile %q at %+v overlaps the avoided rect %+v", tile.Side, at, hole)
		}
		if at.Intersect(whole) != at {
			t.Fatalf("tile %q at %+v leaves the viewport", tile.Side, at)
		}
		if f.Width != at.Cols*cell.Width || f.Height != at.Rows*cell.Height {
			t.Fatalf("tile %q is %dx%d px for %+v cells", tile.Side, f.Width, f.Height, at)
		}
		px := image.Rect(at.Col*cell.Width, at.Row*cell.Height, at.Col*cell.Width+f.Width, at.Row*cell.Height+f.Height)
		draw.Draw(got, px, decode(t, f), image.Point{}, draw.Over)
	}

	frame, err := plan.Frame(badges)
	if err != nil {
		t.Fatal(err)
	}
	want := decode(t, frame)
	for y := 0; y < got.Rect.Dy(); y++ {
		for x := 0; x < got.Rect.Dx(); x++ {
			if (Rect{Row: y / cell.Height, Col: x / cell.Width, Rows: 1, Cols: 1}).overlaps(hole) {
				continue
			}
			if !sameColor(got.At(x, y), want.At(x, y)) {
				t.Fatalf("pixel (%d, %d) differs between the tiles and the frame", x, y)
			}
		}
	}
	for _, b := range badges {
		// The box is hollow, so probe its top edge.
		x, _ := centreOf(b.Row, b.Col)
		y := b.Row * cell.Height
		if a := alphaAt(got, x, y); a == 0 {
			t.Fatalf("the link at (%d, %d) lost its box", b.Row, b.Col)
		}
	}
}

func TestTilesWithoutAnAvoidedRectIsTheWholeFrame(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 40, Rows: 8}
	badges := surrounding()
	plan := planFor(t, badges, viewport)
	tiles, err := plan.Tiles(badges)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiles) != 1 || tiles[0].Side != "" {
		t.Fatalf("got tiles %v, want the one unnamed frame", sides(tiles))
	}
	frame, err := plan.Frame(badges)
	if err != nil {
		t.Fatal(err)
	}
	if got := tiles[0].Frame; got.Row != 0 || got.Col != 0 || got.Rows != frame.Rows || got.Cols != frame.Cols ||
		string(got.PNG) != string(frame.PNG) {
		t.Fatal("the lone tile should be the frame itself")
	}
}

// A side with nothing on it costs no layer.
func TestTilesSkipSidesWithNothingDrawn(t *testing.T) {
	t.Parallel()
	badges := []Badge{{Row: 0, Col: 6, Before: 6, Width: 8, Code: "as"}}
	plan, err := NewPlan(avoiding(badges, Size{Cols: 40, Rows: 8}, hole))
	if err != nil {
		t.Fatal(err)
	}
	tiles, err := plan.Tiles(badges)
	if err != nil {
		t.Fatal(err)
	}
	if got := sides(tiles); len(got) != 1 || got[0] != "top" {
		t.Fatalf("got tiles %v, want only the top", got)
	}
	empty, err := NewPlan(avoiding(nil, Size{Cols: 40, Rows: 8}, hole))
	if err != nil {
		t.Fatal(err)
	}
	if tiles, _ := empty.Tiles(nil); len(tiles) != 0 {
		t.Fatalf("no badges should mean no tiles, got %v", sides(tiles))
	}
}

// A badge would be hidden under the popup, so it moves out from under it,
// and one with nowhere to go is left out rather than shown half hidden.
func TestClipKeepsBadgesOutOfTheAvoidedRect(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 40, Rows: 8}
	moved := []Badge{{Row: 3, Col: 10, Before: 10, Width: 6, Code: "as"}}
	plan, err := NewPlan(avoiding(moved, viewport, hole))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Placed() != 1 {
		t.Fatal("a link beside the popup should keep its badge")
	}
	if at := plan.placed[0].badgeRect(); at.overlaps(hole) {
		t.Fatalf("badge placed at %+v, under the avoided rect %+v", at, hole)
	}

	// A link buried in the middle of the popup, every slot around it covered.
	buried := []Badge{{Row: 3, Col: 18, Before: 18, Width: 4, Code: "as"}}
	plan, err = NewPlan(avoiding(buried, Size{Cols: 40, Rows: 8}, Rect{Rows: 8, Cols: 40}))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Placed() != 0 {
		t.Fatalf("a badge with nowhere visible to go should be dropped, got %+v", plan.placed[0].badgeRect())
	}
}

// A link that runs under the popup keeps its layer on the badge's side.
func TestLayerKeepsClearOfTheAvoidedRect(t *testing.T) {
	t.Parallel()
	badges := []Badge{{Row: 3, Col: 8, Before: 8, Width: 20, Code: "as"}}
	plan, err := NewPlan(avoiding(badges, Size{Cols: 40, Rows: 8}, hole))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Placed() != 1 {
		t.Fatal("want the one badge placed")
	}
	layer, err := plan.Layer(0, badges)
	if err != nil {
		t.Fatal(err)
	}
	at := Rect{Row: layer.Row, Col: layer.Col, Rows: layer.Rows, Cols: layer.Cols}
	if at.overlaps(hole) {
		t.Fatalf("layer at %+v overlaps the avoided rect %+v", at, hole)
	}
	if badge := plan.placed[0].badgeRect(); badge.Intersect(at) != badge {
		t.Fatalf("layer at %+v does not hold the badge at %+v", at, badge)
	}
}

func TestAroundCutsFourBands(t *testing.T) {
	t.Parallel()
	bounds := Rect{Rows: 8, Cols: 40}
	pieces := around(bounds, hole)
	want := map[string]Rect{
		"top":    {Row: 0, Col: 0, Rows: 2, Cols: 40},
		"bottom": {Row: 5, Col: 0, Rows: 3, Cols: 40},
		"left":   {Row: 2, Col: 0, Rows: 3, Cols: 14},
		"right":  {Row: 2, Col: 26, Rows: 3, Cols: 14},
	}
	if len(pieces) != len(want) {
		t.Fatalf("got %d pieces, want %d", len(pieces), len(want))
	}
	for _, p := range pieces {
		if want[p.side] != p.Rect {
			t.Fatalf("%s = %+v, want %+v", p.side, p.Rect, want[p.side])
		}
	}
	// A hole on the edge drops the empty bands.
	edge := around(bounds, Rect{Row: 0, Col: 0, Rows: 3, Cols: 10})
	if got := pieceSides(edge); len(got) != 2 || got[0] != "bottom" || got[1] != "right" {
		t.Fatalf("hole in the corner left %v, want bottom and right", got)
	}
	// A hole clear of the bounds leaves them whole.
	if got := around(bounds, Rect{Row: 20, Col: 0, Rows: 2, Cols: 2}); len(got) != 1 || got[0].side != "" || got[0].Rect != bounds {
		t.Fatalf("a hole outside the bounds changed them: %+v", got)
	}
}

func sides(tiles []Tile) []string {
	out := make([]string, len(tiles))
	for i, tile := range tiles {
		out[i] = tile.Side
	}
	return out
}

func pieceSides(pieces []piece) []string {
	out := make([]string, len(pieces))
	for i, p := range pieces {
		out[i] = p.side
	}
	return out
}

// sameColor compares by channel: a decoded palette entry and a composed
// pixel carry different colour types for the same colour.
func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
