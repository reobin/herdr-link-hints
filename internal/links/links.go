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

// Kind is how a link is re-found: text by URL, OSC8 by anchor.
type Kind int

const (
	Text Kind = iota
	OSC8
)

type Link struct {
	URL  string // ready to open
	Text string // as shown
	Kind Kind
	Row  int // 0-based viewport row
	Col  int // display column
	Pane string
	// Before is blank cells left of the link.
	Before int
}

// Visible is a URL as shown, for re-finding after scroll.
type Visible struct {
	Match string
	Row   int
	Col   int
}

type Match struct {
	URL   string
	Raw   string
	Start int
	End   int
}

const (
	trailingPunctuation = `.,;:!?'"`
	// linkChar excludes whitespace, shell quotes, and C0 controls.
	linkChar = "[^\\s<>\"'`\\\\\x00-\x1f]"
	// hostLabel is one DNS label: no leading or trailing hyphen.
	hostLabel = `[a-z0-9](?:[a-z0-9-]*[a-z0-9])?`
	// hostNeighbours mark a token as path, version, or field access.
	hostNeighbours = `/.-@:~`
)

// schemePrefixes are trusted without boundary checks.
var schemePrefixes = []string{"http:", "https:", "ftp:", "file:", "mailto:"}

var (
	// host is a bare host gated on the TLD list.
	host = hostLabel + `(?:\.` + hostLabel + `)*\.(?:` + tldAlternation + `)`
	// scpPath is owner/repo shaped, without lookahead.
	scpPath = `[\w.-]+/[\w./-]+`

	urlPattern = regexp.MustCompile(
		// (?i) applies to the whole pattern.
		"(?i)" +
			`\b(?:https?|ftp|file|mailto):` + linkChar + `+` +
			`|\b[a-z0-9._-]+@` + host + `:` + scpPath +
			`|\bwww\.` + linkChar + `+` +
			`|\b` + host + `\b(?::[0-9]+)?(?:/` + linkChar + `*)?`,
	)
	scpAnchored  = regexp.MustCompile(`(?i)^[a-z0-9._-]+@(` + host + `):(` + scpPath + `)$`)
	hostAnchored = regexp.MustCompile(`(?i)^` + host + `\b(?::[0-9]+)?(?:/` + linkChar + `*)?$`)
	leadingWord  = regexp.MustCompile(`^\S+`)
	brackets     = []struct{ open, close byte }{{'(', ')'}, {'[', ']'}, {'{', '}'}}
)

// Clean trims prose punctuation but keeps balanced brackets.
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

// Normalize adds a missing scheme or rewrites ssh remotes to https.
// Idempotent, so fresh and stored URLs compare equal.
func Normalize(url string) string {
	if schemed(url) {
		return url
	}
	if strings.HasPrefix(strings.ToLower(url), "www.") {
		return "https://" + url
	}
	if m := scpAnchored.FindStringSubmatch(url); m != nil {
		return "https://" + m[1] + "/" + strings.TrimSuffix(m[2], ".git")
	}
	if hostAnchored.MatchString(url) {
		return "https://" + url
	}
	return url
}

// schemed reports URLs that already name a scheme.
func schemed(url string) bool {
	return strings.Contains(url, "://") || strings.HasPrefix(strings.ToLower(url), "mailto:")
}

func FindAll(line string) []Match {
	return findAll(line, 0)
}

// findAll sweeps from an offset but judges boundaries against the whole line.
func findAll(line string, from int) []Match {
	var out []Match
	for _, loc := range urlPattern.FindAllStringIndex(line[from:], -1) {
		start, end := from+loc[0], from+loc[1]
		raw := line[start:end]
		if needsBoundaries(raw) && !bounded(line, start, end) {
			continue
		}
		out = append(out, Match{URL: Clean(raw), Raw: raw, Start: start - from, End: end - from})
	}
	return out
}

func needsBoundaries(raw string) bool {
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "www.") {
		return false
	}
	for _, scheme := range schemePrefixes {
		if strings.HasPrefix(lower, scheme) {
			return false
		}
	}
	return true
}

// bounded rejects hosts inside larger tokens. \b is ASCII-only and stops at dots.
func bounded(line string, start, end int) bool {
	if start > 0 {
		before, _ := utf8.DecodeLastRuneInString(line[:start])
		if wordRune(before) || strings.ContainsRune(hostNeighbours, before) {
			return false
		}
	}
	if end < len(line) {
		switch line[end] {
		case '-':
			return false
		case '.':
			next, _ := utf8.DecodeRuneInString(line[end+1:])
			if wordRune(next) || next == '_' {
				return false
			}
		}
	}
	return true
}

// Known indexes unwrapped scrollback for cross-line completion.
func Known(text string) map[string]bool {
	known := map[string]bool{}
	for _, m := range FindAll(text) {
		known[m.URL] = true
	}
	return known
}

func MatchLines(lines []string) [][]Match {
	matches := make([][]Match, len(lines))
	for i, line := range lines {
		matches[i] = FindAll(line)
	}
	return matches
}

// FromLines joins URLs wrapped across lines.
func FromLines(lines []string, known map[string]bool) []Visible {
	return FromMatches(lines, MatchLines(lines), known)
}

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
			found = findAll(line, skip)
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

// complete finishes a carried match from known scrollback.
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

// maxAnchorHits caps marks per hidden link.
const maxAnchorHits = 8

// Merge places hidden links by anchor text, falling back to stream coordinates.
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
		if len(at) == maxAnchorHits {
			if c, ok := reportedCell(lines, h, anchor); ok {
				at = []cell{c}
			}
		}
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

// shadowed reports links already placed on a neighbouring cell.
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

// reportedCell trusts stream coordinates only when the anchor is there.
func reportedCell(lines []string, h ansi.Link, anchor string) (cell, bool) {
	if h.Row < 0 || h.Row >= len(lines) {
		return cell{}, false
	}
	line := lines[h.Row]
	at := byteAt(line, h.Col)
	if at < 0 || !strings.HasPrefix(line[at:], anchor) || !wholeToken(line, anchor, at) {
		return cell{}, false
	}
	return cell{h.Row, h.Col}, true
}

// byteAt maps a display column to a byte offset, -1 inside a wide rune.
func byteAt(line string, col int) int {
	if col < 0 {
		return -1
	}
	column := 0
	for i, r := range line {
		if column == col {
			return i
		}
		if column > col {
			return -1
		}
		column += cells.Width(string(r))
	}
	if column == col {
		return len(line)
	}
	return -1
}

// blanksBefore counts empty cells left of a link.
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

// wholeToken rejects anchors inside longer text.
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
