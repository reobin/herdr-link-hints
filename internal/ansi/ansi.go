// Package ansi finds OSC 8 hyperlinks in raw terminal output.
package ansi

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Link is a completed OSC 8 hyperlink. Row and Col are 0-based from the
// stream's origin: Herdr homes the cursor when it replays a repaint, so
// they line up with viewport coordinates.
type Link struct {
	URL   string
	Row   int
	Col   int
	Label string
}

const (
	maxLabelRunes = 80
	tabStop       = 8
)

var (
	csiPattern  = regexp.MustCompile("\x1b\\[([0-9;?]*)?([A-Za-z])")
	osc8Pattern = regexp.MustCompile("\x1b\\]8;;([^\x1b\x07]*?)(?:\x1b\\\\|\x07)")
)

// ParseLinks drops a link whose closing sequence never arrives: without it
// the anchor text, and so the link's extent, is unknown.
func ParseLinks(data []byte) []Link {
	s := string(data)
	cur := cursor{row: 1, col: 1}
	var (
		open  *pending
		found []Link
		idx   int
	)
	for _, esc := range escapes(s) {
		if esc.start > idx {
			cur.writeText(s[idx:esc.start], open)
		}
		idx = esc.end
		switch {
		case esc.kind == kindCSI:
			cur.move(esc.params, esc.final)
		case esc.params != "":
			open = &pending{url: esc.params}
		default:
			if open != nil && open.started {
				found = append(found, open.link())
			}
			open = nil
		}
	}
	if idx < len(s) {
		// Trailing text cannot close an open link, so it emits nothing.
		cur.writeText(s[idx:], open)
	}
	return found
}

type escapeKind int

const (
	kindCSI escapeKind = iota
	kindOSC8
)

type escape struct {
	start  int
	end    int
	kind   escapeKind
	params string
	final  string
}

func escapes(s string) []escape {
	var out []escape
	for _, m := range csiPattern.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, escape{start: m[0], end: m[1], kind: kindCSI, params: group(s, m, 1), final: group(s, m, 2)})
	}
	for _, m := range osc8Pattern.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, escape{start: m[0], end: m[1], kind: kindOSC8, params: group(s, m, 1)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

func group(s string, match []int, n int) string {
	if match[2*n] < 0 {
		return ""
	}
	return s[match[2*n]:match[2*n+1]]
}

type pending struct {
	url     string
	started bool
	row     int
	col     int
	label   string
}

func (p *pending) link() Link {
	return Link{URL: p.url, Row: p.row - 1, Col: p.col - 1, Label: p.label}
}

func (p *pending) write(ch rune, row, col int) {
	if p == nil {
		return
	}
	if !p.started {
		p.started, p.row, p.col = true, row, col
	}
	if utf8.RuneCountInString(p.label) < maxLabelRunes {
		p.label += string(ch)
	}
}

// cursor is 1-based, the way CSI sequences address the screen.
type cursor struct {
	row int
	col int
}

func (c *cursor) writeText(chunk string, open *pending) {
	for _, ch := range chunk {
		switch {
		case ch == '\n':
			c.row++
			c.col = 1
		case ch == '\r':
			c.col = 1
		case ch == '\b':
			if c.col > 1 {
				c.col--
			}
		case ch == '\t':
			c.col += tabStop - ((c.col - 1) % tabStop)
		case ch < ' ':
			// Other control characters do not move the cursor.
		default:
			open.write(ch, c.row, c.col)
			c.col++
		}
	}
}

func (c *cursor) move(params, final string) {
	switch final {
	case "H", "f":
		c.row, c.col = param(params, 0, 1), param(params, 1, 1)
	case "A":
		c.row -= distance(params)
	case "B":
		c.row += distance(params)
	case "C":
		c.col += distance(params)
	case "D":
		c.col -= distance(params)
	case "E":
		c.row += distance(params)
		c.col = 1
	case "F":
		c.row -= distance(params)
		c.col = 1
	case "G":
		c.col = param(params, 0, 1)
	case "d":
		c.row = param(params, 0, 1)
	case "J":
		if strings.HasPrefix(params, "2") {
			c.row, c.col = 1, 1
		}
	}
	if c.row < 1 {
		c.row = 1
	}
	if c.col < 1 {
		c.col = 1
	}
}

func param(params string, i, fallback int) int {
	fields := strings.Split(params, ";")
	if i >= len(fields) {
		return fallback
	}
	n, err := strconv.Atoi(fields[i])
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// distance reads a movement count, where an omitted or zero parameter
// means one cell.
func distance(params string) int {
	if n := param(params, 0, 1); n > 0 {
		return n
	}
	return 1
}
