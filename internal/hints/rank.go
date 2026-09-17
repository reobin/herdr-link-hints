package hints

import (
	"cmp"
	"slices"

	"github.com/reobin/herdr-link-hints/internal/links"
)

// Rank orders targets: focused pane, cursor nearness, recency.
func Rank(found []links.Link, focused string, cursorRows map[string]int) []links.Link {
	ranked := slices.Clone(found)
	slices.SortStableFunc(ranked, func(a, b links.Link) int {
		if c := cmp.Compare(focusedOrder(a.Pane, focused), focusedOrder(b.Pane, focused)); c != 0 {
			return c
		}
		if c := cmp.Compare(cursorDistance(a, cursorRows), cursorDistance(b, cursorRows)); c != 0 {
			return c
		}
		return cmp.Compare(b.Row, a.Row)
	})
	return ranked
}

func focusedOrder(pane, focused string) int {
	if pane == focused {
		return 0
	}
	return 1
}

// cursorDistance counts rows between link and cursor.
func cursorDistance(link links.Link, cursorRows map[string]int) int {
	row, ok := cursorRows[link.Pane]
	if !ok {
		return 0
	}
	d := row - link.Row
	if d < 0 {
		d = -d
	}
	return d
}
