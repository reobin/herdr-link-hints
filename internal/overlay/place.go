package overlay

// placement is a badge resolved to cells.
type placement struct {
	row     int
	col     int
	code    []rune
	linkRow int
	linkCol int
	width   int
	source  int
}

type point struct {
	row int
	col int
}

// clip places badges, dropping ones the viewport cannot hold.
func clip(badges []Badge, viewport Size) []placement {
	taken := linkCells(badges, viewport)
	var out []placement
	for i, b := range badges {
		if b.Row < 0 || b.Row >= viewport.Rows || b.Col < 0 || b.Col >= viewport.Cols {
			continue
		}
		code := []rune(b.Code)
		if len(code) == 0 {
			continue
		}
		row, col := place(b, len(code), viewport, taken)
		// A code cut short is the wrong thing to type.
		if col < 0 || col+len(code) > viewport.Cols {
			continue
		}
		occupy(taken, point{row, col}, len(code))
		out = append(out, placement{
			row:     row,
			col:     col,
			code:    code,
			linkRow: b.Row,
			linkCol: b.Col,
			width:   min(max(b.Width, 1), viewport.Cols-b.Col),
			source:  i,
		})
	}
	return out
}

// linkCells marks cells links cover.
func linkCells(badges []Badge, viewport Size) map[point]bool {
	taken := map[point]bool{}
	for _, b := range badges {
		if b.Row < 0 || b.Row >= viewport.Rows || b.Col < 0 || b.Col >= viewport.Cols {
			continue
		}
		for col := b.Col; col < min(b.Col+max(b.Width, 1), viewport.Cols); col++ {
			taken[point{b.Row, col}] = true
		}
	}
	return taken
}

// place keeps a hint off every link, covering one as a last resort.
func place(b Badge, width int, viewport Size, taken map[point]bool) (row, col int) {
	fallback := point{b.Row, fit(b.Col, width, viewport.Cols)}
	seen := map[point]bool{}
	try := func(s point) (point, bool) {
		if seen[s] {
			return point{}, false
		}
		seen[s] = true
		if free(taken, s, width) {
			return s, true
		}
		return point{}, false
	}
	for _, col := range besideLink(b, width, viewport) {
		if s, ok := try(point{b.Row, col}); ok {
			return s.row, s.col
		}
	}
	for _, colOff := range []int{0, -1, 1, -2, 2, -3, 3} {
		for _, rowOff := range []int{-1, 1, -2, 2} {
			r := b.Row + rowOff
			if r < 0 || r >= viewport.Rows {
				continue
			}
			if s, ok := try(point{r, fit(b.Col+colOff, width, viewport.Cols)}); ok {
				return s.row, s.col
			}
		}
	}
	return fallback.row, fallback.col
}

// besideLink is badge slots on the link's row, nearest first.
func besideLink(b Badge, width int, viewport Size) []int {
	after := b.Col + max(b.Width, 1)
	cols := []int{b.Col - width, after, b.Col - width - 1, after + 1, b.Col - width - 2, after + 2}
	for i, col := range cols {
		cols[i] = fit(col, width, viewport.Cols)
	}
	return cols
}

// fit slides a badge inside the pane.
func fit(col, width, cols int) int {
	return max(0, min(col, cols-width))
}

func occupy(taken map[point]bool, at point, width int) {
	for col := at.col; col < at.col+width; col++ {
		taken[point{at.row, col}] = true
	}
}

func free(taken map[point]bool, at point, width int) bool {
	for col := at.col; col < at.col+width; col++ {
		if taken[point{at.row, col}] {
			return false
		}
	}
	return true
}
