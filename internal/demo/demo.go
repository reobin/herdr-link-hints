// Package demo holds the fixed synthetic pane behind picker --demo.
package demo

import (
	"image/color"
	"strings"

	"github.com/reobin/herdr-link-hints/internal/ansi"
	"github.com/reobin/herdr-link-hints/internal/cells"
	"github.com/reobin/herdr-link-hints/internal/hints"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/theme"
)

const (
	Cols  = 80
	Rows  = 24
	CellW = 9
	CellH = 18
	Pane  = "demo"
)

func Viewport() overlay.Size { return overlay.Size{Cols: Cols, Rows: Rows} }

func Cell() overlay.Cell { return overlay.Cell{Width: CellW, Height: CellH} }

// Colors is a typical dark palette, as a terminal would report it: the
// demo shows the badge in the theme's own accent, not the all-white
// fallback.
func Colors() theme.Colors {
	return theme.Colors{
		Foreground: color.RGBA{R: 0xC0, G: 0xCA, B: 0xF5, A: 0xFF},
		Background: color.RGBA{R: 0x1A, G: 0x1B, B: 0x26, A: 0xFF},
		AccentRed:  color.RGBA{R: 0xF7, G: 0x76, B: 0x8E, A: 0xFF},
		Accent:     color.RGBA{R: 0xE0, G: 0xAF, B: 0x68, A: 0xFF},
		AccentBlue: color.RGBA{R: 0x7A, G: 0xA2, B: 0xF7, A: 0xFF},
	}
}

// Links runs the real scanner over PaneLines, plus one hidden OSC 8 link
// anchored on #232, so every URL the pane shows gets a hint and each
// badge sits where the plugin would put it.
func Links() []links.Link {
	lines := PaneLines()
	hidden := []ansi.Link{{URL: "https://github.com/o/r/pull/232", Label: "#232"}}
	found := links.Merge(lines, links.FromLines(lines, nil), hidden)
	for i := range found {
		found[i].Pane = Pane
	}
	return found
}

func Ranked() []links.Link {
	return hints.Rank(Links(), Pane, map[string]int{Pane: Rows - 1})
}

func Codes() []string {
	return hints.Codes(len(Links()), hints.DefaultAlphabet)
}

func Badges(matches []int, typed string) []overlay.Badge {
	return hints.Badges(Ranked(), Codes(), matches, typed)[Pane]
}

func all() []int {
	n := len(Links())
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func Scene(badges []overlay.Badge) overlay.Scene {
	return overlay.Scene{Badges: badges, Colors: Colors(), Cell: Cell(), Viewport: Viewport()}
}

func SceneFull() overlay.Scene { return Scene(Badges(all(), "")) }

func SceneNarrowed() overlay.Scene { return Scene(Badges([]int{0}, Codes()[0])) }

func SceneTyped() overlay.Scene { return Scene(Badges(all(), Codes()[0])) }

func Scenes() map[string]overlay.Scene {
	return map[string]overlay.Scene{
		"full":     SceneFull(),
		"narrowed": SceneNarrowed(),
		"typed":    SceneTyped(),
	}
}

// PaneLines is the synthetic screen the demo badges sit on. Every link
// text sits at its link's row and column, so a background rendered from
// these lines lines up with the overlay frames.
func PaneLines() []string {
	lines := []string{
		"$ herdr plugin install reobin/herdr-link-hints",
		"installed. reload the server to load it.",
		at(4, "https://a.io/quickstart", "$", " - read this first"),
		"wrote the quickstart in an afternoon.",
		"api docs moved last week.",
		at(10, "https://a.io/api", "docs: ", " reference"),
		"232 closed yesterday.",
		"",
		"repro: open an empty pane, press the key.",
		at(0, "#232", "", " fixes the empty-state crash"),
		"232 is the same issue as above.",
		"",
		at(5, "https://b.io/guide", "read", " first"),
		"it covers install and keys.",
		"$ herdr server reload-config",
		"reloaded.",
		"",
		"examples live on the site.",
		at(30, "www.example.com", "demo pane: six links", " and more"),
		"more links below.",
		"$ open the docs",
		"opened in the browser.",
		at(8, "https://c.io/x", "$", " is nearest the cursor"),
		"$ ",
	}
	out := make([]string, Rows)
	copy(out, lines)
	return out
}

func at(col int, text, prefix, suffix string) string {
	return prefix + strings.Repeat(" ", col-cells.Width(prefix)) + text + suffix
}
