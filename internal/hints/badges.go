package hints

import (
	"github.com/reobin/herdr-link-hints/internal/cells"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/overlay"
)

// Badges dims the links a prefix has ruled out rather than removing
// them, so the screen keeps its shape while the user types, grouped by
// the pane each badge is drawn in.
func Badges(found []links.Link, codes []string, matches []int, typed string) map[string][]overlay.Badge {
	matched := make(map[int]bool, len(matches))
	for _, i := range matches {
		matched[i] = true
	}
	badges := make(map[string][]overlay.Badge, len(found))
	for i, link := range found {
		badges[link.Pane] = append(badges[link.Pane], overlay.Badge{
			Row:    link.Row,
			Col:    link.Col,
			Before: link.Before,
			Width:  cells.Width(link.Text),
			Code:   codes[i],
			Dim:    !matched[i],
			Typed:  commonPrefix(codes[i], typed),
		})
	}
	return badges
}

// commonPrefix counts the leading runes code and typed share, so a ruled-out
// badge never claims more than it matches.
func commonPrefix(code, typed string) int {
	cr, tr := []rune(code), []rune(typed)
	n := 0
	for n < len(cr) && n < len(tr) && cr[n] == tr[n] {
		n++
	}
	return n
}
