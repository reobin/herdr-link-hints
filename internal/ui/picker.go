package ui

import (
	"strings"
)

// Item is a labelled row. The picker knows nothing about links.
type Item struct {
	Code  string
	Text  string
	Where string // pane name, blank when every item shares a pane
	Row   int    // 0-based, shown 1-based
}

type Options struct {
	Title    string
	Alphabet string
}

// reservedRows is the header, prompt and overflow note around the list.
const reservedRows = 6

// Pick selects a code the moment it is unambiguous, so most picks need no
// Enter. The second result is false when the user quits or input ends.
func Pick(t *Terminal, items []Item, opts Options) (int, bool) {
	if !t.interactive() {
		t.render(items, indices(items), "", opts)
		return t.pickByLine(items)
	}
	typed := ""
	for {
		matches := matching(items, typed)
		t.render(items, matches, typed, opts)
		if len(matches) == 1 && typed != "" {
			return matches[0], true
		}
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
