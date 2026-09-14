// Package demo holds the fixed synthetic pane behind picker --demo.
package demo

import (
	"strings"

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

func Colors() theme.Colors { return theme.Fallback() }

func Links() []links.Link {
	return []links.Link{
		{URL: "https://a.io/quickstart", Text: "https://a.io/quickstart", Kind: links.Text, Row: 2, Col: 4, Pane: Pane, Before: 3},
		{URL: "https://a.io/api", Text: "https://a.io/api", Kind: links.Text, Row: 5, Col: 10, Pane: Pane, Before: 4},
		{URL: "https://github.com/o/r/pull/232", Text: "#232", Kind: links.Text, Row: 9, Col: 0, Pane: Pane, Before: 0},
		{URL: "https://b.io/guide", Text: "https://b.io/guide", Kind: links.Text, Row: 12, Col: 20, Pane: Pane, Before: 1},
		{URL: "https://www.example.com", Text: "www.example.com", Kind: links.Text, Row: 18, Col: 30, Pane: Pane, Before: 2},
		{URL: "https://c.io/x", Text: "https://c.io/x", Kind: links.Text, Row: 22, Col: 8, Pane: Pane, Before: 5},
	}
}

func Ranked() []links.Link {
	return hints.Rank(Links(), Pane, map[string]int{Pane: Rows - 1})
}

func Codes() []string {
	return hints.Codes(len(Links()), hints.DefaultAlphabet)
}

func Badges(matches []int, typed string) []overlay.Badge {
	found := Ranked()
	codes := Codes()
	matched := make(map[int]bool, len(matches))
	for _, i := range matches {
		matched[i] = true
	}
	badges := make([]overlay.Badge, len(found))
	for i, link := range found {
		badges[i] = overlay.Badge{
			Row:    link.Row,
			Col:    link.Col,
			Before: link.Before,
			Width:  cells.Width(link.Text),
			Code:   codes[i],
			Dim:    !matched[i],
			Typed:  commonPrefix(codes[i], typed),
		}
	}
	return badges
}

func commonPrefix(code, typed string) int {
	cr, tr := []rune(code), []rune(typed)
	n := 0
	for n < len(cr) && n < len(tr) && cr[n] == tr[n] {
		n++
	}
	return n
}

func all() []int {
	n := len(Links())
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func SceneFull() overlay.Scene {
	return overlay.Scene{Badges: Badges(all(), ""), Colors: Colors(), Cell: Cell(), Viewport: Viewport()}
}

func SceneNarrowed() overlay.Scene {
	return overlay.Scene{Badges: Badges([]int{0}, Codes()[0]), Colors: Colors(), Cell: Cell(), Viewport: Viewport()}
}

func SceneTyped() overlay.Scene {
	codes := Codes()
	badges := Badges(all(), codes[0])
	return overlay.Scene{Badges: badges, Colors: Colors(), Cell: Cell(), Viewport: Viewport()}
}

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
		at(20, "https://b.io/guide", "merged the notes FF", " for details"),
		"the guide covers install and keys.",
		"$ herdr server reload-config",
		"reloaded.",
		"",
		"examples live on the site.",
		at(30, "www.example.com", "demo pane: six links", " and more"),
		"more links below.",
		"$ open https://c.io/x",
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
