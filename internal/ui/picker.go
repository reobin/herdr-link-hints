package ui

import (
	"strconv"
	"strings"
)

// Item is a labelled row.
type Item struct {
	Code string
}

type Options struct {
	Alphabet string
	// OnNarrow redraws hints on match changes.
	OnNarrow func(matches []int, typed string)
}

// Pick selects a code once unambiguous.
func Pick(t *Terminal, items []Item, opts Options) (int, bool) {
	// Piped input reads a code per line.
	if !t.interactive() {
		narrow(opts, indices(items), "")
		return t.pickByLine(items)
	}
	typed := ""
	shown := ""
	first := true
	for {
		matches := matching(items, typed)
		// Decide before drawing; that frame would tear down at once.
		if len(matches) == 1 && typed != "" {
			return matches[0], true
		}
		if first || typed != shown {
			narrow(opts, matches, typed)
			first, shown = false, typed
		}
		t.renderStatus(len(matches), typed)
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

// pickByLine reads one code per line.
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

func (t *Terminal) renderStatus(matches int, typed string) {
	t.centred(echo(typed), count(matches, t.cols))
}

// Blank before the first keystroke keeps the count from moving.
func echo(typed string) line {
	return line{{text: typed, sgr: "1"}}
}

// Past three digits the unit goes rather than wrapping.
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
		total += len([]rune(s.text))
	}
	return total
}

// Rounding left pad up holds the edge still as the count grows.
func centrePad(width, text int) int {
	return max((width-text+1)/2, 0)
}

// Rounding down centres the count, not the block.
func topPad(rows, lines int) int {
	return max((rows-lines)/2, 0)
}
