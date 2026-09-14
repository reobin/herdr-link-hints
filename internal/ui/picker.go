package ui

import (
	"strconv"
	"strings"
)

// Item is a labelled row.
type Item struct {
	Code  string
	Text  string
	Where string // pane name, blank when every item shares a pane
	Row   int    // 0-based, shown 1-based
}

// Style is how the picker presents itself: a status line when hints are
// drawn on the panes, a list when they cannot be.
type Style int

const (
	StyleList Style = iota
	StyleStatus
)

type Options struct {
	Title    string
	Alphabet string
	Style    Style
	// OnNarrow runs whenever the match set changes, so a caller can redraw
	// hints the picker knows nothing about.
	OnNarrow func(matches []int, typed string)
}

// reservedRows is the header, prompt and overflow note around the list.
const reservedRows = 6

// Pick selects a code the moment it is unambiguous, so most picks need no
// Enter. The second result is false when the user quits or input ends.
func Pick(t *Terminal, items []Item, opts Options) (int, bool) {
	if !t.interactive() {
		all := indices(items)
		narrow(opts, all, "")
		t.render(items, all, "", opts)
		return t.pickByLine(items)
	}
	typed := ""
	shown := ""
	first := true
	for {
		matches := matching(items, typed)
		// Deciding before drawing: that frame would be torn down at once,
		// on the one keystroke the user is waiting for.
		if len(matches) == 1 && typed != "" {
			return matches[0], true
		}
		if first || typed != shown {
			narrow(opts, matches, typed)
			first, shown = false, typed
		}
		t.render(items, matches, typed, opts)
		switch k := t.readKey(); k.kind {
		case keyEnd, keyEscape:
			return 0, false
		case keyBackspace:
			if typed != "" {
				typed = typed[:len(typed)-1]
			}
		case keyEnter:
			if len(matches) == 1 {
				return matches[0], true
			}
		case keyRune:
			if strings.ContainsRune(opts.Alphabet, k.r) {
				typed += string(k.r)
			}
		}
	}
}

func narrow(opts Options, matches []int, typed string) {
	if opts.OnNarrow != nil {
		opts.OnNarrow(matches, typed)
	}
}

// pickByLine takes one whole code per line, for a stdin that is not a
// terminal.
func (t *Terminal) pickByLine(items []Item) (int, bool) {
	line, err := t.lines.ReadString('\n')
	code := strings.TrimSpace(line)
	if code == "" || (err != nil && line == "") {
		return 0, false
	}
	for i, item := range items {
		if item.Code == code {
			return i, true
		}
	}
	return 0, false
}

func matching(items []Item, typed string) []int {
	var out []int
	for i, item := range items {
		if strings.HasPrefix(item.Code, typed) {
			out = append(out, i)
		}
	}
	return out
}

func indices(items []Item) []int {
	out := make([]int, len(items))
	for i := range items {
		out[i] = i
	}
	return out
}

func (t *Terminal) render(items []Item, matches []int, typed string, opts Options) {
	if opts.Style == StyleStatus {
		t.renderStatus(len(matches), typed)
		return
	}
	t.renderList(items, matches, typed, opts)
}

func (t *Terminal) renderStatus(matches int, typed string) {
	t.centred(echo(typed), count(matches, t.cols))
}

// Blank before the first keystroke rather than a prompt glyph: the row is
// reserved either way, which is what stops the count moving when typing
// starts.
func echo(typed string) line {
	return line{{text: typed, sgr: "1"}}
}

// The theme's own yellow marks a prefix that has ruled every hint out. The
// box is a few cells wide, so past three digits the unit goes rather than
// wrapping onto the row below.
func count(matches, width int) line {
	if matches == 0 {
		return line{{text: "no match", sgr: "33"}}
	}
	unit := " links"
	if matches == 1 {
		unit = " link"
	}
	number := strconv.Itoa(matches)
	if len(number)+len(unit) > width {
		return line{{text: number, sgr: "1"}}
	}
	return line{{text: number, sgr: "1"}, {text: unit, sgr: "2"}}
}

// Sized from what the pane reports, not what was asked for: Herdr floors a
// popup at more rows than it is given.
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

// A line knows its own width: its escape codes are not part of what the
// reader sees.
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
		total += len([]rune(s.text))
	}
	return total
}

// Rounding the left pad up holds the left edge still as the count gains a
// digit.
func centrePad(width, text int) int {
	return max((width-text+1)/2, 0)
}

// Rounding down centres the count rather than the block: it is the last
// line, so a spare row above would carry it off the middle.
func topPad(rows, lines int) int {
	return max((rows-lines)/2, 0)
}

func (t *Terminal) renderList(items []Item, matches []int, typed string, opts Options) {
	t.Clear()
	t.Printf("Link hints (%d links, %s).\n", len(items), opts.Title)
	t.Printf("Type a hint, Enter opens, Esc quits.\n\n")

	shown := matches
	hidden := 0
	if limit := t.rows - reservedRows; limit > 0 && len(shown) > limit {
		hidden = len(shown) - limit
		shown = shown[:limit]
	}
	for _, i := range shown {
		item := items[i]
		where := ""
		if item.Where != "" {
			where = item.Where + " "
		}
		t.Printf("  %s  %s [%sr%d]\n", item.Code, item.Text, where, item.Row+1)
	}
	if hidden > 0 {
		t.Printf("  ... %d more, keep typing to narrow\n", hidden)
	}
	if len(matches) == 0 {
		t.Printf("\nNo match. Backspace to edit, Esc to quit.\n")
	}
	t.Printf("\n> %s", typed)
	t.Flush()
}
