// Package demo holds the synthetic pane behind picker --demo.
package demo

import (
	"strings"

	"github.com/reobin/herdr-link-hints/internal/ansi"
	"github.com/reobin/herdr-link-hints/internal/cells"
	"github.com/reobin/herdr-link-hints/internal/hints"
	"github.com/reobin/herdr-link-hints/internal/links"
)

const (
	Cols = 80
	Rows = 24
	Pane = "demo"
)

// Links runs the real scanner over PaneLines, plus one hidden OSC 8 link.
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

// PaneLines is the synthetic demo screen.
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
