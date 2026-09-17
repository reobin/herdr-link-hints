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

const (
	trailingPunctuation = `.,;:!?'"`
	// linkChar is what a URL may run over: anything but whitespace, the
	// characters a shell quotes with, and C0 controls.
	linkChar = "[^\\s<>\"'`\\\\\x00-\x1f]"
	// hostLabel is one DNS label, which may not open or close on a hyphen.
	hostLabel = `[a-z0-9](?:[a-z0-9-]*[a-z0-9])?`
	// hostNeighbours are the characters that make a token a path, a version
	// or a field access rather than a host. \b does not rule them out.
	hostNeighbours = `/.-@:~`
)

// schemePrefixes mirrors what browse will open. A match starting with one of
// these, or with www., is trusted as it always was; only the bare-host and ssh
// shapes pay for the boundary filters.
var schemePrefixes = []string{"http:", "https:", "ftp:", "file:", "mailto:"}

var (
	// host is a bare host gated on the TLD list: the gate is the only thing
	// keeping main.go and v1.2.3 off the screen.
	host = hostLabel + `(?:\.` + hostLabel + `)*\.(?:` + tldAlternation + `)`
	// scpPath is deliberately owner/repo shaped. It is what keeps an ssh
	// port out of "git@host:22", keeps "git@host:~/notes" from normalizing
	// into a URL nobody can open, and lets the branch do without the
	// lookahead RE2 does not have.
	scpPath = `[\w.-]+/[\w./-]+`

	urlPattern = regexp.MustCompile(
		// (?i) here runs to the end of the pattern, so every branch below
		// is case-insensitive too. www. stays ahead of the bare host so a
		// www. match keeps the extent it has always had.
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

// Normalize supplies the scheme a host leaves out, and rewrites an ssh remote
// into the https form that is the only one a browser can open. It recognizes
// a shape or leaves the string alone: Merge and shadowed run it over every OSC
// 8 target too, and those carry schemes of their own that are none of our
// business. It is also idempotent, which is what lets Locate compare a freshly
// read token against a URL normalized a keystroke ago.
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

// schemed keeps a URL that already says where it goes off the host rules. The
// "://" test cannot be loosened to a bare colon: a host carries one too, in
// example.com:8080.
func schemed(url string) bool {
	return strings.Contains(url, "://") || strings.HasPrefix(strings.ToLower(url), "mailto:")
}

func FindAll(line string) []Match {
	return findAll(line, 0)
}

// findAll sweeps from a byte offset but reads its left context out of the
// whole line, so a match resuming after a carry is judged by what really
// precedes it. Offsets stay relative to the sweep, which is what the caller
// measures columns against.
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

// bounded rejects a host that is really part of something else. \b is an ASCII
// word boundary, which leaves both ends open: on the left it reads the
// continuation byte of "bücher.de" as a break and matches "cher.de", and on
// the right it is satisfied by the dot in "java.io.IOException", so the engine
// settles for the prefix that happens to end in a TLD. A dot followed by a
// letter is that case; a dot followed by anything else ends a sentence.
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

// reportedCell takes the observe stream at its word once the anchor sweep has
// hit its cap. Below the cap every occurrence still gets its own hint, because
// a hint has to be on the copy you are looking at; past it the sweep is
// guessing, and one cell the stream and the snapshot agree on beats eight
// truncated at an arbitrary place. The agreement is the whole test: a
// coordinate sitting on its own anchor is right whatever produced it, which is
// more than sizing the replay can promise.
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

// byteAt turns a display column back into a byte offset, and reports -1 for a
// column falling inside a wide rune: there is no offset there, and answering
// with the next rune's would verify the wrong text.
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
