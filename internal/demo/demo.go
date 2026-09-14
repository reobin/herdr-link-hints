// Package demo holds the fixed synthetic pane behind picker --demo.
package demo

import (
	"github.com/reobin/herdr-link-hints/internal/cells"
	"github.com/reobin/herdr-link-hints/internal/hints"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/theme"
)

const (
	Cols  = 80
	Rows  = 24
	CellW = 8
	CellH = 16
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
