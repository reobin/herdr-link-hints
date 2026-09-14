// Command frames writes the demo overlay scenes as PNGs at a chosen cell
// size, so scripts/build-demo-gif.sh can composite them at the gif's resolution
// instead of upscaling the test goldens. It also writes demo.env, the
// pane geometry and palette as shell assignments, so the script carries
// no copy of the Go constants.
package main

import (
	"flag"
	"fmt"
	"image/color"
	"os"
	"path/filepath"

	"github.com/reobin/herdr-link-hints/internal/demo"
	"github.com/reobin/herdr-link-hints/internal/overlay"
)

func main() {
	out := flag.String("out", ".", "directory to write <scene>.png and demo.env into")
	cellW := flag.Int("cell-width", demo.CellW, "cell width in pixels")
	cellH := flag.Int("cell-height", demo.CellH, "cell height in pixels")
	flag.Parse()
	if err := run(*out, overlay.Cell{Width: *cellW, Height: *cellH}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out string, cell overlay.Cell) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for name, scene := range demo.Scenes() {
		scene.Cell = cell
		frame, err := overlay.Render(scene)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(out, name+".png"), frame.PNG, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "demo.env"), []byte(env()), 0o644)
}

func env() string {
	colors := demo.Colors()
	return fmt.Sprintf("COLS=%d\nROWS=%d\nBG=%s\nFG=%s\nCODE0=%s\nURL0=%s\n",
		demo.Cols, demo.Rows, hex(colors.Background), hex(colors.Foreground), demo.Codes()[0], demo.Ranked()[0].URL)
}

func hex(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}
