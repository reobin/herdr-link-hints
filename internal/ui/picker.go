package ui

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/reobin/herdr-link-hints/internal/cells"
)

// Item is one listed link: its URL.
type Item struct {
	URL string
}

// Pick selects a row: move the selection and confirm with Enter.
func Pick(t *Terminal, items []Item) (int, bool) {
	// Piped input reads a URL per line.
	if !t.interactive() {
		return t.pickByLine(items)
	}
	sel := 0
	for {
		sel = clampSel(sel, len(items))
		t.renderList(items, sel)
		switch k := t.readKey(); k.kind {
		case keyEnd, keyEscape:
			return 0, false
		case keyEnter:
			if len(items) > 0 {
				return sel, true
			}
		case keyUp:
			if sel > 0 {
				sel--
			}
		case keyDown:
			if sel < len(items)-1 {
				sel++
			}
		case keyRune:
			switch k.r {
			case 'j':
				if sel < len(items)-1 {
					sel++
				}
			case 'k':
				if sel > 0 {
					sel--
				}
			}
		}
	}
}

// clampSel keeps the selection on the list.
func clampSel(sel, total int) int {
	if total == 0 {
		return 0
	}
	return min(max(sel, 0), total-1)
}

// pickByLine reads one URL per line.
func (t *Terminal) pickByLine(items []Item) (int, bool) {
	line, err := t.lines.ReadString('\n')
	url := strings.TrimSpace(line)
	if url == "" || (err != nil && line == "") {
		return 0, false
	}
	for i, item := range items {
		if item.URL == url {
			return i, true
		}
	}
	return 0, false
}

// renderList draws the status, the visible window, and the key help
// pinned to the last row.
func (t *Terminal) renderList(items []Item, sel int) {
	l := layout{
		width: t.cols,
		pad:   sidePad(t.cols),
	}
	height, spaced := listHeight(t.rows)
	lines := []string{sprintLine(l.statusLine(len(items)))}
	if spaced {
		lines = append(lines, "")
	}
	if len(items) == 0 && height > 0 {
		lines = append(lines, sprintLine(l.emptyLine()))
	}
	for _, row := range window(len(items), sel, height) {
		lines = append(lines, sprintLine(l.itemLine(items[row], row == sel)))
	}
	if t.rows >= 3 {
		for len(lines) < t.rows-1 {
			lines = append(lines, "")
		}
		lines = append(lines, sprintLine(l.footerLine()))
	}
	t.Clear()
	t.Printf("%s", strings.Join(lines, "\r\n"))
	t.Flush()
}

// layout is the column plan every line shares, so the columns hold
// still as the list narrows.
type layout struct {
	width int
	pad   int
}

const gap = "  "

// sidePad breathes against the popup edge unless the popup is clamped.
func sidePad(width int) int {
	if width >= 40 {
		return 1
	}
	return 0
}

// listHeight is the rows left for matches under the status and above
// the footer, and whether a blank line separates them from the status.
func listHeight(rows int) (height int, spaced bool) {
	switch {
	case rows >= 6:
		return rows - 3, true
	case rows >= 3:
		return rows - 2, false
	default:
		return max(rows-1, 0), false
	}
}

// window is the rows on screen, kept around the selection.
func window(total, sel, height int) []int {
	if height <= 0 {
		return nil
	}
	if total <= height {
		out := make([]int, 0, max(total, 0))
		for i := 0; i < total; i++ {
			out = append(out, i)
		}
		return out
	}
	start := min(max(sel-height/2, 0), total-height)
	out := make([]int, 0, height)
	for i := start; i < start+height; i++ {
		out = append(out, i)
	}
	return out
}

// statusLine is the link count on the left.
func (l layout) statusLine(total int) line {
	return l.spread(line{{text: count(total), sgr: "2"}}, nil)
}

// count is the whole list.
func count(total int) string {
	if total == 1 {
		return "1 link"
	}
	return strconv.Itoa(total) + " links"
}

// emptyLine stands in for the list when there is nothing to show.
func (l layout) emptyLine() line {
	return truncateLine(append(l.indent(), segment{text: "no links", sgr: "2"}), l.width)
}

// footerLine is the key help, shortened when the popup is clamped.
func (l layout) footerLine() line {
	help := footerHelp(false)
	if !l.fits(help, nil) {
		help = footerHelp(true)
	}
	return l.spread(help, nil)
}

// footerHelp pairs each key with its action the way TUIs usually do:
// the key bright, what it does dim.
func footerHelp(short bool) line {
	keys := []string{"↑↓/jk", "enter", "esc"}
	labels := []string{"move", "open", "quit"}
	var out line
	for i, key := range keys {
		if i > 0 {
			out = append(out, segment{text: "   "})
		}
		out = append(out, segment{text: key, sgr: "1"})
		if !short {
			out = append(out, segment{text: " " + labels[i], sgr: "2"})
		}
	}
	return out
}

// itemLine is one row: its URL. The selected row inverts edge to edge
// so it reads without colour support.
func (l layout) itemLine(item Item, selected bool) line {
	room := l.width - 2*l.pad
	url := truncate(sanitize(item.URL), room)
	out := append(l.indent(), segment{text: url})
	out = truncateLine(out, l.width)
	if !selected {
		return out
	}
	if fill := l.width - out.width(); fill > 0 {
		out = append(out, segment{text: strings.Repeat(" ", fill)})
	}
	return invert(out)
}

// invert swaps every segment to reverse video, dropping dim so the bar
// stays legible.
func invert(l line) line {
	for i := range l {
		if l[i].sgr == "1" {
			l[i].sgr = "7;1"
		} else {
			l[i].sgr = "7"
		}
	}
	return l
}

func (l layout) indent() line {
	if l.pad == 0 {
		return nil
	}
	return line{{text: strings.Repeat(" ", l.pad)}}
}

func (l layout) fits(left, right line) bool {
	if right.width() == 0 {
		return left.width() <= l.width-2*l.pad
	}
	return left.width()+len(gap)+right.width() <= l.width-2*l.pad
}

// spread sets left and right at the edges, dropping the right when it
// does not fit.
func (l layout) spread(left, right line) line {
	room := l.width - 2*l.pad
	out := l.indent()
	if right.width() == 0 || !l.fits(left, right) {
		return append(out, truncateLine(left, room)...)
	}
	out = append(out, left...)
	out = append(out, segment{text: strings.Repeat(" ", room-left.width()-right.width())})
	return append(out, right...)
}

// sanitize drops control runes so an untrusted OSC 8 target cannot move
// the cursor or split rows. Measurement already counts them as zero
// width, but the terminal would still interpret them.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// truncate shortens to whole cells, marking the cut.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if cells.Width(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		if w+cells.Width(string(r)) > max(width-1, 0) {
			break
		}
		b.WriteRune(r)
		w += cells.Width(string(r))
	}
	return b.String() + "…"
}

// truncateLine drops trailing segments past the width.
func truncateLine(l line, width int) line {
	var out line
	w := 0
	for _, s := range l {
		if w >= width {
			break
		}
		if w+cells.Width(s.text) > width {
			s.text = truncate(s.text, width-w)
		}
		out = append(out, s)
		w += cells.Width(s.text)
	}
	return out
}

func sprintLine(l line) string {
	var b strings.Builder
	for _, s := range l {
		if s.sgr == "" {
			b.WriteString(s.text)
		} else {
			b.WriteString("\x1b[" + s.sgr + "m" + s.text + "\x1b[0m")
		}
	}
	return b.String()
}

// A line knows its width without escape codes.
type line []segment

type segment struct {
	text string
	sgr  string
}

func (l line) String() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString("\x1b[" + s.sgr + "m" + s.text + "\x1b[0m")
	}
	return b.String()
}

func (l line) width() int {
	total := 0
	for _, s := range l {
		total += cells.Width(s.text)
	}
	return total
}

// Sized from what the pane reports, not what was asked for.
func (t *Terminal) centred(lines ...line) {
	t.Clear()
	t.Printf("%s", strings.Repeat("\n", topPad(t.rows, len(lines))))
	for i, l := range lines {
		if i > 0 {
			t.Printf("\n")
		}
		t.Printf("%s%s", strings.Repeat(" ", centrePad(t.cols, l.width())), l)
	}
	t.Flush()
}

// Rounding left pad up holds the edge still as the count grows.
func centrePad(width, text int) int {
	return max((width-text+1)/2, 0)
}

// Rounding down centres the count, not the block.
func topPad(rows, lines int) int {
	return max((rows-lines)/2, 0)
}
