package ui

import (
	"bufio"
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/reobin/herdr-link-hints/internal/cells"
)

// keyTerminal drives a Terminal from a fixed byte stream.
func keyTerminal(t *testing.T, input []byte) (*Terminal, *bytes.Buffer) {
	t.Helper()
	keys := make(chan byte, len(input))
	for _, b := range input {
		keys <- b
	}
	close(keys)
	var out bytes.Buffer
	return &Terminal{out: bufio.NewWriter(&out), keys: keys, rows: fallbackRows, cols: fallbackCols}, &out
}

func lineTerminal(input string) (*Terminal, *bytes.Buffer) {
	var out bytes.Buffer
	return &Terminal{out: bufio.NewWriter(&out), lines: bufio.NewReader(strings.NewReader(input))}, &out
}

func TestReadKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input []byte
		want  key
	}{
		{"printable", []byte("a"), key{kind: keyRune, r: 'a'}},
		{"carriage return", []byte("\r"), key{kind: keyEnter}},
		{"newline", []byte("\n"), key{kind: keyEnter}},
		{"delete", []byte{0x7f}, key{kind: keyBackspace}},
		{"ctrl-p moves up", []byte{0x10}, key{kind: keyUp}},
		{"ctrl-n moves down", []byte{0x0e}, key{kind: keyDown}},
		{"ctrl-c quits", []byte{0x03}, key{kind: keyEscape}},
		{"ctrl-d quits", []byte{0x04}, key{kind: keyEscape}},
		{"lone escape quits", []byte{0x1b}, key{kind: keyEscape}},
		{"end of input", nil, key{kind: keyEnd}},
		{"control bytes are skipped", []byte{0x01, 'a'}, key{kind: keyRune, r: 'a'}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			term, _ := keyTerminal(t, tc.input)
			if got := term.readKey(); got != tc.want {
				t.Fatalf("ReadKey() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Plain arrows move the selection; anything fancier stays unrecognised.
func TestReadKeyArrowKeys(t *testing.T) {
	t.Parallel()
	arrows := []struct {
		sequence string
		want     keyKind
	}{
		{"\x1b[A", keyUp},
		{"\x1b[B", keyDown},
		{"\x1bOA", keyUp},
		{"\x1bOB", keyDown},
		{"\x1b[1;5A", keyUp},
		{"\x1b[1;5B", keyDown},
	}
	for _, tc := range arrows {
		term, _ := keyTerminal(t, []byte(tc.sequence))
		if got := term.readKey(); got.kind != tc.want {
			t.Errorf("ReadKey(%q) = %+v, want %+v", tc.sequence, got, tc.want)
		}
	}
	for _, sequence := range []string{"\x1b[1;5C", "\x1bOP", "\x1b[200~"} {
		term, _ := keyTerminal(t, []byte(sequence))
		if got := term.readKey(); got.kind != keyUnknown {
			t.Errorf("ReadKey(%q) = %+v, want keyUnknown", sequence, got)
		}
	}
}

func TestReadKeyEscapeThenTypingStillQuits(t *testing.T) {
	t.Parallel()
	// Esc followed by a plain character is not a sequence, so Esc wins.
	term, _ := keyTerminal(t, []byte("\x1ba"))
	if got := term.readKey(); got.kind != keyEscape {
		t.Fatalf("ReadKey() = %+v, want keyEscape", got)
	}
}

func TestReadKeySlowEscapeIsNotASequence(t *testing.T) {
	t.Parallel()
	keys := make(chan byte, 2)
	keys <- 0x1b
	go func() {
		time.Sleep(escapeSequenceWait * 3)
		keys <- '['
	}()
	var out bytes.Buffer
	term := &Terminal{out: bufio.NewWriter(&out), keys: keys, rows: fallbackRows}
	if got := term.readKey(); got.kind != keyEscape {
		t.Fatalf("ReadKey() = %+v, want keyEscape for a lone Esc", got)
	}
}

func items(codes ...string) []Item {
	out := make([]Item, len(codes))
	for i, code := range codes {
		out[i] = Item{Code: code, URL: "https://x.io/" + code}
	}
	return out
}

func TestPick(t *testing.T) {
	t.Parallel()
	opts := Options{Alphabet: "asdfghjkl"}
	tests := []struct {
		name  string
		input string
		want  int
		ok    bool
	}{
		{"unambiguous code selects without Enter", "sa", 3, true},
		{"backspace edits", "sd\x7fa", 3, true},
		{"escape quits", "\x1b", 0, false},
		{"arrow key does not quit", "\x1b[Asa", 3, true},
		{"down then Enter opens the highlighted row", "\x1b[B\r", 1, true},
		{"up at the top stays", "\x1b[A\r", 0, true},
		{"down past the end stays", "\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B\r", 4, true},
		{"typing resets the selection to the head", "\x1b[B\x1b[Bs\r", 3, true},
		{"input running out quits", "s", 0, false},
		{"keys outside the alphabet are ignored", "zsqa", 3, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			term, _ := keyTerminal(t, []byte(tc.input))
			got, ok := Pick(term, items("aa", "as", "ad", "sa", "ss"), opts)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Fatalf("Pick() = %d, %v; want %d, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestPickEnterConfirmsASingleMatch(t *testing.T) {
	t.Parallel()
	term, _ := keyTerminal(t, []byte("\r"))
	got, ok := Pick(term, items("a"), Options{Alphabet: "asdfghjkl"})
	if !ok || got != 0 {
		t.Fatalf("Pick() = %d, %v", got, ok)
	}
}

// A fully typed short code selects at once: prefix-free codes have no
// longer sibling waiting on another keystroke.
func TestPickSelectsAShortCodeWithoutEnter(t *testing.T) {
	t.Parallel()
	term, _ := keyTerminal(t, []byte("a"))
	got, ok := Pick(term, items("a", "sa", "ss"), Options{Alphabet: "asdfghjkl"})
	if !ok || got != 0 {
		t.Fatalf("Pick() = %d, %v, want the short code without Enter", got, ok)
	}
}

func TestPickByLine(t *testing.T) {
	t.Parallel()
	term, _ := lineTerminal("ad\n")
	got, ok := Pick(term, items("aa", "as", "ad"), Options{Alphabet: "asdfghjkl"})
	if !ok || got != 2 {
		t.Fatalf("Pick() = %d, %v", got, ok)
	}

	term, _ = lineTerminal("zz\n")
	if _, ok := Pick(term, items("aa"), Options{Alphabet: "asdfghjkl"}); ok {
		t.Fatal("an unknown code should not select anything")
	}
}

// The list shows the status, one row per match with code and URL, and
// the key help on the last row with the slack above it. The selection
// inverts edge to edge.
func TestRenderListShowsStatusRowsAndHelp(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.rows, term.cols = 10, 60
	term.renderList(items("a", "s"), []int{0, 1}, 1, "")
	term.Flush()

	text := stripSGR(out.String())
	rows := strings.Split(text, "\r\n")
	want := []string{
		" 2 links",
		"",
		" a  https://x.io/a",
		" s  https://x.io/s" + strings.Repeat(" ", 42),
		"", "", "", "", "",
		" ↑↓ move   enter open   esc quit",
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("rendered list:\n%s\nwant:\n%s", text, strings.Join(want, "\r\n"))
	}
	selected := strings.Split(out.String(), "\r\n")[3]
	if !strings.HasPrefix(selected, "\x1b[7m") || strings.Contains(selected, "\x1b[2m") || strings.Contains(selected, "\x1b[0m ") {
		t.Fatalf("selected row is not inverted edge to edge:\n%q", selected)
	}
}

// The status says how much of the list a typed prefix keeps.
func TestRenderListCountsTheNarrowedList(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.rows, term.cols = 6, 40
	term.renderList(items("aa", "as", "ad", "sa", "ss", "sd"), []int{0, 1, 2}, 0, "a")
	term.Flush()
	if text := stripSGR(out.String()); !strings.Contains(text, "3 of 6") {
		t.Fatalf("rendered list has no count:\n%s", text)
	}
}

// A clamped popup drops the side padding, shortens the help, and keeps
// the code when the URL cannot fit.
func TestRenderListAdaptsToASmallPopup(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.rows, term.cols = 3, 20
	term.renderList(items("a"), []int{0}, 0, "")
	term.Flush()
	rows := strings.Split(stripSGR(out.String()), "\r\n")
	want := []string{"1 link", "a  https://x.io/a" + strings.Repeat(" ", 3), "↑↓   enter   esc"}
	if !slices.Equal(rows, want) {
		t.Fatalf("rows = %q, want %q", rows, want)
	}
}

// Nothing matching is a readout, not a crash: the count goes to zero
// and the list names the prefix.
func TestRenderListSaysWhenNothingMatches(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.renderList(items("a"), nil, 0, "z")
	term.Flush()
	text := stripSGR(out.String())
	for _, want := range []string{"0 of 1", "no link starts with z"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered list has no %q:\n%s", want, text)
		}
	}
}

// Long URLs fit the popup, marked where they are cut.
func TestRenderListTruncatesToThePopup(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.rows, term.cols = 5, 30
	long := Item{Code: "a", URL: "https://x.io/a-very-long-path-here"}
	term.renderList([]Item{long}, []int{0}, 0, "")
	term.Flush()
	for _, row := range strings.Split(stripSGR(out.String()), "\r\n") {
		if cells.Width(strings.TrimSpace(row)) > 30 {
			t.Fatalf("row over the popup width: %q", row)
		}
	}
	if text := stripSGR(out.String()); !strings.Contains(text, "https://x.io/a-very") {
		t.Fatalf("URL was cut short:\n%s", text)
	}
}

// The window follows the selection so it never scrolls out of sight.
func TestWindowKeepsSelectionVisible(t *testing.T) {
	t.Parallel()
	matches := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if got := window(matches, 0, 4); !slices.Equal(got, []int{0, 1, 2, 3}) {
		t.Fatalf("window() = %v", got)
	}
	if got := window(matches, 9, 4); !slices.Equal(got, []int{6, 7, 8, 9}) {
		t.Fatalf("window() = %v", got)
	}
	if got := window(matches, 5, 4); len(got) != 4 || !slices.Contains(got, 5) {
		t.Fatalf("window() = %v, want the selection on screen", got)
	}
	if got := window(matches, 0, 99); !slices.Equal(got, matches) {
		t.Fatalf("window() = %v, want every match", got)
	}
	if got := window(nil, 0, 4); len(got) != 0 {
		t.Fatalf("window() = %v, want nothing", got)
	}
}

// stripSGR drops every CSI sequence, the clear as well as the styling.
func stripSGR(s string) string {
	var b strings.Builder
	for {
		before, rest, found := strings.Cut(s, "\x1b[")
		b.WriteString(before)
		if !found {
			return b.String()
		}
		end := strings.IndexFunc(rest, func(r rune) bool { return r >= '@' && r <= '~' })
		s = rest[end+1:]
	}
}

// A pane nothing is typed into should not show a cursor waiting for input.
func TestOpenHidesTheCursor(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.restore = func() {}
	term.hideCursor()
	term.Close()
	text := out.String()
	if !strings.HasPrefix(text, "\x1b[?25l") {
		t.Fatalf("cursor was never hidden: %q", text)
	}
	if !strings.HasSuffix(text, "\x1b[?25h") {
		t.Fatalf("cursor was never put back: %q", text)
	}
}

// The spinner owns the terminal until it is stopped.
func TestSpin(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.rows, term.cols = 3, 20
	stop := term.Spin("scanning")
	stop()
	term.Flush()

	text := out.String()
	if !strings.Contains(text, "scanning") {
		t.Fatalf("Spin() drew no label: %q", text)
	}
	if !strings.Contains(text, spinnerFrames[0]) {
		t.Fatalf("Spin() drew no spinner: %q", text)
	}
	before := out.Len()
	time.Sleep(3 * spinnerTick)
	term.Flush()
	if out.Len() != before {
		t.Fatal("Spin() kept drawing after it was stopped")
	}
}

// A pipe has no place to animate, so the label is stated once.
func TestSpinOnAPipeSaysItOnce(t *testing.T) {
	t.Parallel()
	term, out := lineTerminal("")
	term.Spin("scanning")()
	term.Flush()
	if got := out.String(); got != "scanning\r\n" {
		t.Fatalf("Spin() on a pipe wrote %q", got)
	}
}

// A late OSC reply arriving as input must not type its payload.
func TestReadKeySwallowsALateColorReply(t *testing.T) {
	t.Parallel()
	for _, sequence := range []string{
		"\x1b]11;rgb:cdcd/c9c9/c5c5\a",
		"\x1b]10;rgb:3a3a/3636/3232\x1b\\",
	} {
		term, _ := keyTerminal(t, []byte(sequence))
		if got := term.readKey(); got.kind != keyUnknown {
			t.Errorf("ReadKey(%q) = %+v, want keyUnknown", sequence, got)
		}
	}
}

// The annotate pane is a few cells wide, so the label goes rather than
// being cut in half.
func TestSpinnerTextDropsALabelThatCannotFit(t *testing.T) {
	t.Parallel()
	term, _ := keyTerminal(t, nil)

	term.cols = 40
	if got := term.spinnerText(0, "scanning"); got != spinnerFrames[0]+" scanning" {
		t.Fatalf("spinnerText() = %q, want the label kept", got)
	}
	term.cols = 3
	if got := term.spinnerText(0, "scanning"); got != spinnerFrames[0] {
		t.Fatalf("spinnerText() = %q, want the spinner alone", got)
	}
}

// The wider gap falls on the right, so the left edge stays put.
func TestCentrePadRoundsUp(t *testing.T) {
	t.Parallel()
	cases := []struct{ width, text, want int }{
		{10, 7, 2}, // odd leftover: two left, one right
		{9, 7, 1},  // even leftover: symmetric
		{4, 8, 0},  // wider than the pane
	}
	for _, tc := range cases {
		if got := centrePad(tc.width, tc.text); got != tc.want {
			t.Fatalf("centrePad(%d, %d) = %d, want %d", tc.width, tc.text, got, tc.want)
		}
	}
}
