package overlay

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/theme"
)

// drawOneGlyph fills a cell-sized image with bg and draws r in fg, so tests
// can read back the exact palette indices a glyph wrote.
func drawOneGlyph(cell Cell, r rune, fg, bg uint8, dim, inverted bool) *image.Paletted {
	img := image.NewPaletted(image.Rect(0, 0, cell.Width, cell.Height), newPalette(theme.Fallback()))
	fill(img, img.Rect, bg)
	drawGlyph(img, r, 0, 0, cell, fg, dim, inverted)
	return img
}

func usesBlends(img *image.Paletted) bool {
	for _, index := range img.Pix {
		if index >= aaBase {
			return true
		}
	}
	return false
}

// At the retina cell from the issue the edges blend into the background
// instead of stepping in whole blocks.
func TestSmoothGlyphBlendsEdgesAtScaleThree(t *testing.T) {
	t.Parallel()
	img := drawOneGlyph(Cell{Width: 18, Height: 38}, 'a', colorText, colorBackground, false, false)
	if !usesBlends(img) {
		t.Fatal("a scale 3 glyph wrote no edge blends")
	}
	foundCore := false
	for _, index := range img.Pix {
		if index == colorText {
			foundCore = true
		}
	}
	if !foundCore {
		t.Fatal("a smoothed glyph lost its full-coverage core")
	}
}

// A typed prefix inverts the glyph against its cell, and a ruled-out badge
// fades: both still blend, each against their own background.
func TestSmoothGlyphBlendsEachRamp(t *testing.T) {
	t.Parallel()
	big := Cell{Width: 18, Height: 38}
	cases := []struct {
		name          string
		fg, bg        uint8
		dim, inverted bool
		pair          uint8
	}{
		{"bright", colorText, colorBackground, false, false, aaBright},
		{"typed", colorBackground, colorText, false, true, aaBrightInverted},
		{"dim", colorDimText, colorDimBackground, true, false, aaDim},
		{"dim typed", colorDimBackground, colorDimText, true, true, aaDimInverted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			img := drawOneGlyph(big, 'a', tc.fg, tc.bg, tc.dim, tc.inverted)
			blends := false
			for _, index := range img.Pix {
				if index < aaBase {
					continue
				}
				blends = true
				if got := (index - aaBase) / aaLevels; got != tc.pair {
					t.Fatalf("blend index %d sits on ramp %d, want %d", index, got, tc.pair)
				}
			}
			if !blends {
				t.Fatal("no edge blends written")
			}
		})
	}
}

// Below the smoothing scale a glyph writes flat foreground only, exactly as
// before.
func TestHardGlyphWritesNoBlendsAtScaleOne(t *testing.T) {
	t.Parallel()
	img := drawOneGlyph(Cell{Width: 9, Height: 19}, 'a', colorText, colorBackground, false, false)
	if usesBlends(img) {
		t.Fatal("a scale 1 glyph wrote edge blends")
	}
}

var cell = Cell{Width: 8, Height: 16}

func scene(badges []Badge, viewport Size) Scene {
	return Scene{Badges: badges, Colors: theme.Fallback(), Cell: cell, Viewport: viewport}
}

func decode(t *testing.T, frame Frame) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(frame.PNG))
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return img
}

func alphaAt(img image.Image, x, y int) uint32 {
	_, _, _, a := img.At(x, y).RGBA()
	return a >> 8
}

// centreOf is a pixel well inside a cell, away from borders and rules.
func centreOf(row, col int) (int, int) {
	return col*cell.Width + cell.Width/2, row*cell.Height + cell.Height/2
}

func render(t *testing.T, badges []Badge, viewport Size) (Frame, image.Image) {
	t.Helper()
	frame, err := Render(scene(badges, viewport))
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	return frame, decode(t, frame)
}

func TestRenderCoversTheWholeViewport(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 80, Rows: 24}
	frame, _ := render(t, []Badge{{Row: 4, Col: 10, Before: 4, Width: 6, Code: "as"}}, viewport)
	if frame.Row != 0 || frame.Col != 0 {
		t.Fatalf("frame origin = (%d, %d); want (0, 0)", frame.Row, frame.Col)
	}
	if frame.Rows != viewport.Rows || frame.Cols != viewport.Cols {
		t.Fatalf("frame grid = %dx%d; want %dx%d", frame.Rows, frame.Cols, viewport.Rows, viewport.Cols)
	}
	if frame.Width != viewport.Cols*cell.Width || frame.Height != viewport.Rows*cell.Height {
		t.Fatalf("frame pixels = %dx%d", frame.Width, frame.Height)
	}
}

// The pane is not dimmed: the overlay draws the badge and its rule and
// leaves every other pixel, the link's own cells included, untouched.
func TestRenderLeavesThePaneAlone(t *testing.T) {
	t.Parallel()
	_, img := render(t, []Badge{{Row: 4, Col: 10, Before: 4, Width: 6, Code: "as"}}, Size{Cols: 80, Rows: 24})

	for _, at := range []struct{ row, col int }{{20, 40}, {4, 12}, {0, 0}, {4, 20}} {
		x, y := centreOf(at.row, at.col)
		if a := alphaAt(img, x, y); a != 0 {
			t.Fatalf("cell (%d, %d) has alpha %#x; want the pane left clear", at.row, at.col, a)
		}
	}
	x, y := centreOf(4, 8)
	if a := alphaAt(img, x, y); a != 0xFF {
		t.Fatalf("the badge at (%d, %d) has alpha %#x; want it drawn", 4, 8, a)
	}
}

// The link is boxed on all four sides, not just underlined: a link a few
// cells wide gets too little rule to read as marked now that nothing dims.
func TestRenderBoxesTheLink(t *testing.T) {
	t.Parallel()
	_, img := render(t, []Badge{{Row: 4, Col: 10, Before: 4, Width: 6, Code: "as"}}, Size{Cols: 80, Rows: 24})
	top, bottom := 4*cell.Height, (4+1)*cell.Height-1
	left, right := 10*cell.Width, 16*cell.Width-1
	midX, midY := 13*cell.Width+cell.Width/2, 4*cell.Height+cell.Height/2

	for _, edge := range []struct {
		name string
		x, y int
	}{
		{"top", midX, top},
		{"bottom", midX, bottom},
		{"left", left, midY},
		{"right", right, midY},
	} {
		if a := alphaAt(img, edge.x, edge.y); a != 0xFF {
			t.Fatalf("no %s edge on the link box: alpha %#x", edge.name, a)
		}
	}
	// Hollow, so the link text still reads through it.
	if a := alphaAt(img, midX, midY); a != 0 {
		t.Fatalf("the box is filled at its centre: alpha %#x", a)
	}
	if a := alphaAt(img, 16*cell.Width+cell.Width/2, bottom); a == 0xFF {
		t.Fatal("the box runs past the end of the link")
	}
}

func TestRenderDrawsTheBadgeBesideTheLink(t *testing.T) {
	t.Parallel()
	_, img := render(t, []Badge{{Row: 4, Col: 10, Before: 4, Width: 6, Code: "as"}}, Size{Cols: 80, Rows: 24})
	for _, col := range []int{8, 9} {
		x, y := centreOf(4, col)
		if a := alphaAt(img, x, y); a != 0xFF {
			t.Fatalf("badge cell %d is not opaque: alpha %#x", col, a)
		}
	}
}

// A ruled-out hint fades rather than disappearing, so the screen stays put.
func TestRenderFadesARuledOutBadge(t *testing.T) {
	t.Parallel()
	_, img := render(t, []Badge{
		{Row: 4, Col: 10, Before: 4, Width: 6, Code: "as"},
		{Row: 8, Col: 10, Before: 4, Width: 6, Code: "df", Dim: true},
	}, Size{Cols: 80, Rows: 24})

	x, y := centreOf(4, 8)
	bright := alphaAt(img, x, y)
	x, y = centreOf(8, 8)
	faded := alphaAt(img, x, y)
	if bright != 0xFF {
		t.Fatalf("the matching badge is not opaque: alpha %#x", bright)
	}
	if faded == 0 || faded >= bright {
		t.Fatalf("the ruled-out badge has alpha %#x; want it faded but drawn", faded)
	}
}

// The typed prefix is inverted against the rest of the badge, so the screen
// shows what the readout echoed.
func TestRenderInvertsTheTypedPrefix(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 80, Rows: 24}
	// Before is 4 and the code is two characters, so the badge sits beside
	// the link at columns 8-9. y=66 is inside the badge box, below its
	// outline and above the centred glyph.
	_, img := render(t, []Badge{{Row: 4, Col: 10, Before: 4, Width: 6, Code: "as", Typed: 1}}, viewport)
	r1, g1, b1, _ := img.At(8*cell.Width+cell.Width/2, 4*cell.Height+2).RGBA()
	r2, g2, b2, _ := img.At(9*cell.Width+cell.Width/2, 4*cell.Height+2).RGBA()
	if r1 == r2 && g1 == g2 && b1 == b2 {
		t.Fatal("the typed cell blends into the rest of the badge")
	}

	_, plain := render(t, []Badge{{Row: 4, Col: 10, Before: 4, Width: 6, Code: "as"}}, viewport)
	p1, q1, _, _ := plain.At(8*cell.Width+cell.Width/2, 4*cell.Height+2).RGBA()
	p2, q2, _, _ := plain.At(9*cell.Width+cell.Width/2, 4*cell.Height+2).RGBA()
	if p1 != p2 || q1 != q2 {
		t.Fatal("a badge with no typed prefix should read as one box")
	}
}

func TestRenderClipsToTheViewport(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 80, Rows: 24}
	_, img := render(t, []Badge{
		{Row: 40, Col: 2, Width: 4, Code: "as"},  // below the viewport
		{Row: 2, Col: 200, Width: 4, Code: "sd"}, // right of it
		{Row: -1, Col: 2, Width: 4, Code: "df"},  // above it
	}, viewport)
	if x, y, ok := drawn(img); ok {
		t.Fatalf("a badge outside the viewport was drawn at (%d, %d)", x, y)
	}
}

func TestRenderSlidesABadgeInAtTheRightEdge(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 80, Rows: 24}
	_, img := render(t, []Badge{{Row: 3, Col: 79, Before: 0, Width: 1, Code: "as"}}, viewport)
	// Beside the link, slid left so the whole code fits: a code cut short
	// is the wrong code to type.
	for _, col := range []int{77, 78} {
		x, y := centreOf(3, col)
		if a := alphaAt(img, x, y); a != 0xFF {
			t.Fatalf("the badge is not drawn at column %d: alpha %#x", col, a)
		}
	}
}

// Without a badge the frame is still the whole viewport - Herdr is given
// the placement the badges were drawn against - but it draws nothing.
func TestRenderDrawsNothingWithoutBadges(t *testing.T) {
	t.Parallel()
	viewport := Size{Cols: 80, Rows: 24}
	frame, img := render(t, nil, viewport)
	if frame.Rows != viewport.Rows || frame.Cols != viewport.Cols {
		t.Fatalf("frame grid = %dx%d; want %dx%d", frame.Rows, frame.Cols, viewport.Rows, viewport.Cols)
	}
	if x, y, ok := drawn(img); ok {
		t.Fatalf("something is drawn at (%d, %d)", x, y)
	}
}

// drawn finds the first pixel the overlay put anything on.
func drawn(img image.Image) (int, int, bool) {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if alphaAt(img, x, y) != 0 {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

// The badge takes the palette entry with the most contrast against the
// background, so a light theme does not get yellow-on-cream.
func TestPalettePicksTheHighestContrastAccent(t *testing.T) {
	t.Parallel()
	light := theme.Colors{
		Background: color.RGBA{R: 0xFD, G: 0xF6, B: 0xE3, A: 0xFF},
		AccentRed:  color.RGBA{R: 0xDC, G: 0x32, B: 0x2F, A: 0xFF},
		Accent:     color.RGBA{R: 0xB5, G: 0x89, B: 0x00, A: 0xFF},
		AccentBlue: color.RGBA{R: 0x26, G: 0x8B, B: 0xD2, A: 0xFF},
	}
	want, score := theme.BestAccent(light)
	if score < theme.MinBadgeContrast {
		t.Fatalf("fixture winner = %.2f:1, want at least %.1f:1", score, theme.MinBadgeContrast)
	}
	got := newPalette(light)
	if got[colorBackground] != want {
		t.Fatalf("badge background = %+v, want %+v", got[colorBackground], want)
	}
	if got[colorBackground] == (color.RGBA{R: 0xB5, G: 0x89, B: 0x00, A: 0xFF}) {
		t.Fatal("badge background kept the low-contrast yellow")
	}
}

func TestRenderRejectsAnUnknownCellSize(t *testing.T) {
	t.Parallel()
	zeroCell := scene([]Badge{{Code: "a"}}, Size{Cols: 80, Rows: 24})
	zeroCell.Cell = Cell{}
	if _, err := Render(zeroCell); err == nil {
		t.Fatal("Render() with a zero cell returned no error")
	}
	if _, err := Render(scene([]Badge{{Code: "a"}}, Size{})); err == nil {
		t.Fatal("Render() with a zero viewport returned no error")
	}
}

func TestCheckSizeRejectsAnOversizeFrame(t *testing.T) {
	t.Parallel()
	if err := checkSize(maxFrameBytes); err != nil {
		t.Fatalf("checkSize(%d) = %v; want no error", maxFrameBytes, err)
	}
	err := checkSize(maxFrameBytes + 1)
	if err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("checkSize(%d) = %v; want an over-size error", maxFrameBytes+1, err)
	}
}

// The worst case a real pane can produce still has to fit under the cap.
func TestRenderKeepsAFullScreenUnderTheCap(t *testing.T) {
	t.Parallel()
	frame, err := Render(fullScreenScene())
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if len(frame.PNG) > maxFrameBytes/2 {
		t.Fatalf("a full screen encodes to %d bytes, too near the %d cap", len(frame.PNG), maxFrameBytes)
	}
}

// Badges stay on the link row unless pushed off.
func TestPlaceKeepsTheHintOffTheLink(t *testing.T) {
	t.Parallel()
	wide := Size{Cols: 80, Rows: 24}
	cases := []struct {
		name     string
		badge    Badge
		viewport Size
		wantRow  int
		wantCol  int
	}{
		{"room beside the link", Badge{Row: 5, Col: 10, Before: 4, Code: "as"}, wide, 5, 8},
		{"exactly enough room", Badge{Row: 5, Col: 10, Before: 2, Code: "as"}, wide, 5, 8},
		{"one space fits one character", Badge{Row: 5, Col: 10, Before: 1, Code: "a"}, wide, 5, 9},
		{"one space does not fit two", Badge{Row: 5, Col: 10, Before: 1, Code: "as"}, wide, 5, 8},
		{"flush against a word", Badge{Row: 5, Col: 10, Before: 0, Code: "a"}, wide, 5, 9},
		{"the top row stays on its row", Badge{Row: 0, Col: 10, Before: 0, Code: "as"}, wide, 0, 8},
		{"the right edge slides the badge in", Badge{Row: 5, Col: 79, Before: 0, Code: "as"}, wide, 5, 77},
		{"a one-row pane has nowhere to go", Badge{Row: 0, Col: 10, Before: 0, Code: "as"}, Size{Cols: 80, Rows: 1}, 0, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row, col := place(tc.badge, len([]rune(tc.badge.Code)), tc.viewport, nil)
			if row != tc.wantRow || col != tc.wantCol {
				t.Fatalf("place(%+v) = %d, %d; want %d, %d", tc.badge, row, col, tc.wantRow, tc.wantCol)
			}
		})
	}
}

// Two hints on the same cells read as one code that selects neither.
func TestClipKeepsBadgesOffEachOther(t *testing.T) {
	t.Parallel()
	wide := Size{Cols: 80, Rows: 24}
	// Same row, one cell apart: both want the row above, and there is not
	// room up there for two two-character codes.
	placed := clip([]Badge{
		{Row: 5, Col: 10, Width: 1, Code: "as"},
		{Row: 5, Col: 11, Width: 1, Code: "df"},
	}, wide, Rect{})
	if len(placed) != 2 {
		t.Fatalf("clip() placed %d badges, want 2", len(placed))
	}
	taken := map[point]bool{}
	for _, p := range placed {
		for col := p.col; col < p.col+len(p.code); col++ {
			at := point{p.row, col}
			if taken[at] {
				t.Fatalf("two badges share %+v: %+v", at, placed)
			}
			taken[at] = true
		}
	}
}

func TestGlyphIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	lower, ok := glyph('a')
	if !ok {
		t.Fatal("glyph('a') is missing")
	}
	upper, ok := glyph('A')
	if !ok {
		t.Fatal("glyph('A') is missing")
	}
	if lower != upper {
		t.Fatal("glyph('a') and glyph('A') differ")
	}
}

// Every letter has to be told apart at five pixels wide.
func TestEveryGlyphIsDistinct(t *testing.T) {
	t.Parallel()
	seen := make(map[[glyphHeight]byte]rune, len(glyphs))
	for r, bitmap := range glyphs {
		if other, clash := seen[bitmap]; clash {
			t.Fatalf("%q and %q are the same bitmap", r, other)
		}
		seen[bitmap] = r
	}
}

// A descender is what separates g from a and p from n.
func TestDescendersReachBelowTheBaseline(t *testing.T) {
	t.Parallel()
	for _, r := range "gjpqy" {
		bitmap, ok := glyph(r)
		if !ok {
			t.Fatalf("no glyph for %q", r)
		}
		if bitmap[glyphHeight-1] == 0 {
			t.Fatalf("%q has no descender", r)
		}
	}
	for _, r := range "aeimnorsuvwxz" {
		bitmap, _ := glyph(r)
		if bitmap[glyphHeight-1] != 0 {
			t.Fatalf("%q sits below the baseline", r)
		}
	}
}

func TestEveryDefaultAlphabetLetterHasAGlyph(t *testing.T) {
	t.Parallel()
	for _, r := range "asdfghjklqwertyuiopzxcvbnm0123456789" {
		if _, ok := glyph(r); !ok {
			t.Fatalf("no glyph for %q", r)
		}
	}
}

func TestPlaceAvoidsANeighboursLink(t *testing.T) {
	t.Parallel()
	wide := Size{Cols: 80, Rows: 24}
	above := Badge{Row: 4, Col: 10, Width: 4, Code: "sd"}
	badge := Badge{Row: 5, Col: 10, Width: 4, Code: "as"}
	taken := linkCells([]Badge{above, badge}, wide)
	// Its own row is free, so it takes the cells beside its link rather
	// than the row above, where covering the neighbour would hide the only
	// way to select it.
	row, col := place(badge, 2, wide, taken)
	if row != 5 || col != 8 {
		t.Fatalf("place() = %d, %d; want 5, 8", row, col)
	}
	for c := col; c < col+2; c++ {
		if taken[point{row, c}] {
			t.Fatalf("place() = %d, %d, which covers a link cell", row, col)
		}
	}
}

// Three badges on one row must all clear every link.
func TestClipPlacesThreeBadgesOnOneRow(t *testing.T) {
	t.Parallel()
	wide := Size{Cols: 80, Rows: 24}
	badges := []Badge{
		{Row: 5, Col: 10, Width: 1, Code: "as"},
		{Row: 5, Col: 11, Width: 1, Code: "df"},
		{Row: 5, Col: 12, Width: 1, Code: "gh"},
	}
	placed := clip(badges, wide, Rect{})
	if len(placed) != 3 {
		t.Fatalf("clip() placed %d badges, want 3", len(placed))
	}
	links := linkCells(badges, wide)
	used := map[point]bool{}
	for _, p := range placed {
		for col := p.col; col < p.col+len(p.code); col++ {
			at := point{p.row, col}
			if links[at] {
				t.Fatalf("badge %+v covers a link cell %+v", p, at)
			}
			if used[at] {
				t.Fatalf("two badges share %+v: %+v", at, placed)
			}
			used[at] = true
		}
	}
}

// Packed rows push the badge two rows out.
func TestPlaceReachesTheSecondRow(t *testing.T) {
	t.Parallel()
	wide := Size{Cols: 80, Rows: 24}
	badges := []Badge{
		{Row: 5, Col: 0, Width: 10, Code: "jk"},
		{Row: 5, Col: 14, Width: 66, Code: "kl"},
		{Row: 4, Col: 8, Width: 6, Code: "sd"},
		{Row: 5, Col: 10, Width: 4, Code: "as"},
		{Row: 6, Col: 8, Width: 6, Code: "df"},
	}
	taken := linkCells(badges, wide)
	if row, col := place(badges[3], 2, wide, taken); row != 3 || col != 10 {
		t.Fatalf("place() = %d, %d; want 3, 10", row, col)
	}
}

// When every aligned column is taken, the badge shifts a few cells aside
// on a nearby row instead of overlapping.
func TestPlaceShiftsSidewaysWhenAlignedIsTaken(t *testing.T) {
	t.Parallel()
	wide := Size{Cols: 80, Rows: 24}
	badge := Badge{Row: 5, Col: 10, Width: 4, Code: "as"}
	taken := map[point]bool{}
	for col := range wide.Cols {
		taken[point{5, col}] = true
	}
	for _, row := range []int{3, 4, 6, 7} {
		taken[point{row, 10}] = true
		taken[point{row, 11}] = true
	}
	if row, col := place(badge, 2, wide, taken); row != 4 || col != 8 {
		t.Fatalf("place() = %d, %d; want 4, 8", row, col)
	}
}
