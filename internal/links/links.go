// Package links extracts openable targets from a terminal screen.
package links

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/reobin/herdr-link-hints/internal/ansi"
	"github.com/reobin/herdr-link-hints/internal/cells"
)

// Kind decides how a link can be found again later: plain text can be
// searched for by URL, an OSC 8 target never appears on screen.
type Kind int

const (
	Text Kind = iota
	OSC8
)

type Link struct {
	URL  string // where it goes, ready for a browser
	Text string // what the screen shows
	Kind Kind
	Row  int // 0-based viewport row
	Col  int // 0-based display column, not a byte offset
	Pane string
	// Before counts the blank cells left of the link, which is the room a
	// hint has beside it.
	Before int
}

// Visible holds a URL as it appears on screen, so it can be searched for
// again after the pane scrolls.
type Visible struct {
	Match string
	Row   int
	Col   int
}

type Match struct {
	URL   string // trimmed of trailing prose punctuation
	Raw   string
	Start int
	End   int
}

const trailingPunctuation = `.,;:!?'"`

var (
	urlPattern  = regexp.MustCompile("(?i)\\b(?:https?|ftp|file|mailto):[^\\s<>\"'`\\\\\x00-\x1f]+|\\bwww\\.[^\\s<>\"'`\\\\\x00-\x1f]+")
	leadingWord = regexp.MustCompile(`^\S+`)
	brackets    = []struct{ open, close byte }{{'(', ')'}, {'[', ']'}, {'{', '}'}}
)

// Clean keeps a closing bracket the URL opened itself, so targets like
// .../Foo_(disambiguation) survive.
func Clean(url string) string {
	for {
		trimmed := strings.TrimRight(url, trailingPunctuation)
		if unbalanced(trimmed) {
			trimmed = trimmed[:len(trimmed)-1]
		}
		if trimmed == url {
			return url
		}
		url = trimmed
	}
}

func unbalanced(url string) bool {
	if url == "" {
		return false
	}
	last := url[len(url)-1]
	for _, pair := range brackets {
		if last == pair.close && strings.Count(url, string(pair.open)) < strings.Count(url, string(pair.close)) {
			return true
		}
	}
	return false
}

// Normalize supplies the scheme a bare www. host leaves out.
func Normalize(url string) string {
	if strings.HasPrefix(strings.ToLower(url), "www.") {
		return "https://" + url
	}
	return url
}

func FindAll(line string) []Match {
	var out []Match
	for _, loc := range urlPattern.FindAllStringIndex(line, -1) {
		raw := line[loc[0]:loc[1]]
		out = append(out, Match{URL: Clean(raw), Raw: raw, Start: loc[0], End: loc[1]})
	}
	return out
}

// Known indexes unwrapped scrollback so a URL broken across visible lines
// can be completed from the real thing instead of guessed at.
func Known(text string) map[string]bool {
	known := map[string]bool{}
	for _, m := range FindAll(text) {
		known[m.URL] = true
	}
	return known
}

// MatchLines runs the URL pattern over each line once, so callers that
// need the matches more than once do not pay for the sweep again.
func MatchLines(lines []string) [][]Match {
	matches := make([][]Match, len(lines))
	for i, line := range lines {
		matches[i] = FindAll(line)
	}
	return matches
}

// FromLines carries a URL that runs to the end of a line into the next
// one, rather than blindly joining whatever follows it.
func FromLines(lines []string, known map[string]bool) []Visible {
	return FromMatches(lines, MatchLines(lines), known)
}

// FromMatches is FromLines over matches already found. A carry makes the
// next line resume past what the completion consumed, and only then is the
// line swept again.
func FromMatches(lines []string, matches [][]Match, known map[string]bool) []Visible {
	var (
		out     []Visible
		carried *Visible
	)
	for i, line := range lines {
		skip := 0
		if carried != nil {
			var completed Visible
			completed, skip = complete(*carried, line, known)
			out = append(out, completed)
			carried = nil
		}
		if skip > len(line) {
			skip = len(line)
		}
		found := matches[i]
		if skip > 0 {
			found = FindAll(line[skip:])
		}
		for _, m := range found {
			start := cells.Column(line, skip+m.Start)
			if skip+m.End == len(line) && i+1 < len(lines) {
				carried = &Visible{Match: m.Raw, Row: i, Col: start}
				continue
			}
			out = append(out, Visible{Match: m.URL, Row: i, Col: start})
		}
	}
	if carried != nil {
		out = append(out, Visible{Match: Clean(carried.Match), Row: carried.Row, Col: carried.Col})
	}
	return out
}

// complete reports how many bytes of this line the continuation consumed.
// A carry ending in trailing punctuation is prose until proven otherwise,
// so it never completes from known: the joined text built from that guess
// must not confirm it.
func complete(carried Visible, line string, known map[string]bool) (Visible, int) {
	if Clean(carried.Match) != carried.Match {
		carried.Match = Clean(carried.Match)
		return carried, 0
	}
	full := longestWithPrefix(known, carried.Match)
	if full == "" {
		carried.Match = Clean(carried.Match)
		return carried, 0
	}
	carried.Match = full
	if line == "" || isSpace(line[0]) {
		return carried, 0
	}
	return carried, len(leadingWord.FindString(line))
}

func longestWithPrefix(known map[string]bool, prefix string) string {
	best := ""
	for url := range known {
		if len(url) > len(prefix) && len(url) > len(best) && strings.HasPrefix(url, prefix) {
			best = url
		}
	}
	return best
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

type cell struct {
	row int
	col int
}

// maxAnchorHits caps how often one hidden link may be marked, so a label as
// short as "#2" cannot flood a screen with hints.
const maxAnchorHits = 8

// Merge places a hidden link by searching the visible text for its anchor:
// the observe stream is a repaint of its own, so it can be staler than the
// snapshot and its coordinates are the last resort. Every occurrence is
// marked. A last-resort placement yields to a snapshot-placed link for the
// same target on a neighbouring cell: that pair is one visual link seen
// before and after output arrived mid-scan, not two links.
func Merge(lines []string, visible []Visible, hidden []ansi.Link) []Link {
	var out []Link
	taken := map[cell]bool{}
	add := func(link Link) {
		at := cell{link.Row, link.Col}
		if taken[at] {
			return
		}
		taken[at] = true
		link.Before = blanksBefore(lines, link.Row, link.Col)
		out = append(out, link)
	}
	var fallback []ansi.Link
	for _, h := range hidden {
		anchor := anchorText(h)
		at := anchorCells(lines, anchor)
		if len(at) == 0 {
			fallback = append(fallback, h)
			continue
		}
		for _, c := range at {
			add(Link{URL: h.URL, Text: anchor, Kind: OSC8, Row: c.row, Col: c.col})
		}
	}
	for _, v := range visible {
		add(Link{URL: Normalize(v.Match), Text: v.Match, Kind: Text, Row: v.Row, Col: v.Col})
	}
	for _, h := range fallback {
		if shadowed(out, h) {
			continue
		}
		add(Link{URL: h.URL, Text: anchorText(h), Kind: OSC8, Row: h.Row, Col: h.Col})
	}
	return out
}

// shadowed reports whether the snapshot already placed the same target on
// a neighbouring cell: the fallback coordinates are then stale output,
// not a second link.
func shadowed(placed []Link, h ansi.Link) bool {
	for _, link := range placed {
		if Normalize(link.URL) != Normalize(h.URL) {
			continue
		}
		if abs(link.Row-h.Row) <= 1 && abs(link.Col-h.Col) <= 1 {
			return true
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func anchorCells(lines []string, anchor string) []cell {
	if anchor == "" {
		return nil
	}
	var out []cell
	for row, line := range lines {
		for at := 0; at < len(line); {
			i := strings.Index(line[at:], anchor)
			if i < 0 {
				break
			}
			if !wholeToken(line, anchor, at+i) {
				at += i + len(anchor)
				continue
			}
			out = append(out, cell{row, cells.Column(line, at+i)})
			if len(out) == maxAnchorHits {
				return out
			}
			at += i + len(anchor)
		}
	}
	return out
}

// blanksBefore counts the empty cells immediately left of a link, which is
// where its hint can sit without covering the link itself.
func blanksBefore(lines []string, row, col int) int {
	if row < 0 || row >= len(lines) || col <= 0 {
		return 0
	}
	blanks, column := 0, 0
	for _, r := range lines[row] {
		if column >= col {
			break
		}
		if r == ' ' {
			blanks++
		} else {
			blanks = 0
		}
		column += cells.Width(string(r))
	}
	// A link past the end of the text has nothing but blanks before it.
	return blanks + max(col-column, 0)
}

func anchorText(h ansi.Link) string {
	if h.Label != "" && h.Label != h.URL {
		return h.Label
	}
	return h.URL
}

// wholeToken rejects an anchor that is only part of a longer run of text, so
// a link labelled #1 does not claim the #1 inside #123.
func wholeToken(line, anchor string, start int) bool {
	first, _ := utf8.DecodeRuneInString(anchor)
	last, _ := utf8.DecodeLastRuneInString(anchor)
	before, _ := utf8.DecodeLastRuneInString(line[:start])
	after, _ := utf8.DecodeRuneInString(line[start+len(anchor):])
	return breaks(before, first) && breaks(last, after)
}

func breaks(left, right rune) bool {
	return !wordRune(left) || !wordRune(right)
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
