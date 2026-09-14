package demo

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/cells"
	"github.com/reobin/herdr-link-hints/internal/overlay"
)

func TestRankedIsDeterministic(t *testing.T) {
	t.Parallel()
	first, second := Ranked(), Ranked()
	if len(first) != 6 || len(second) != 6 {
		t.Fatalf("Ranked() len = %d, %d; want 6, 6", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("Ranked() run 1 [%d] = %+v, run 2 = %+v", i, first[i], second[i])
		}
	}
	if first[0].URL != "https://c.io/x" {
		t.Fatalf("Ranked()[0].URL = %q, want the link nearest the cursor", first[0].URL)
	}
	seen := map[string]bool{}
	for _, c := range Codes() {
		if seen[c] {
			t.Fatalf("Codes() has a duplicate %q", c)
		}
		seen[c] = true
	}
}

func TestPaneLinesAnchorTheLinks(t *testing.T) {
	t.Parallel()
	lines := PaneLines()
	if len(lines) != Rows {
		t.Fatalf("PaneLines() has %d lines, want %d", len(lines), Rows)
	}
	for _, link := range Links() {
		line := lines[link.Row]
		if cells.Width(line) > Cols {
			t.Fatalf("row %d is %d cells wide, over the %d viewport", link.Row, cells.Width(line), Cols)
		}
		start := cells.Width(line[:colByte(line, link.Col)])
		if start != link.Col {
			t.Fatalf("row %d: %d cells precede col %d", link.Row, start, link.Col)
		}
		if rest := line[colByte(line, link.Col):]; !strings.HasPrefix(rest, link.Text) {
			t.Fatalf("row %d col %d: %q does not start with %q", link.Row, link.Col, rest, link.Text)
		}
	}
}

func colByte(line string, col int) int {
	width := 0
	for i, r := range line {
		if width >= col {
			return i
		}
		width += cells.Width(string(r))
	}
	return len(line)
}

func TestPaneFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join("testdata", "pane.txt")
	want := strings.Join(PaneLines(), "\n") + "\n"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pane file: %v (run with UPDATE_GOLDEN=1)", err)
	}
	if string(got) != want {
		t.Fatal("pane.txt does not match PaneLines() (run with UPDATE_GOLDEN=1)")
	}
}

func TestGolden(t *testing.T) {
	t.Parallel()
	for name, scene := range Scenes() {
		frame, err := overlay.Render(scene)
		if err != nil {
			t.Fatalf("%s: Render() error: %v", name, err)
		}
		path := filepath.Join("testdata", "golden", name+".png")
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, frame.PNG, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: read golden: %v (run with UPDATE_GOLDEN=1)", name, err)
		}
		if !samePixels(decodePNG(t, frame.PNG), decodePNG(t, want)) {
			t.Fatalf("%s: rendered frame differs from the golden (run with UPDATE_GOLDEN=1)", name)
		}
	}
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return img
}

// Goldens are compared as pixels: the encoder's deflate output can change
// between Go releases while the frame does not.
func samePixels(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				return false
			}
		}
	}
	return true
}
