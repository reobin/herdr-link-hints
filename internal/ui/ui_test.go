package ui

import (
	"bufio"
	"bytes"
	"image/color"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/reobin/herdr-link-hints/internal/theme"
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

// An arrow key used to read as a bare Esc and close the picker.
func TestReadKeyEscapeSequences(t *testing.T) {
	t.Parallel()
	for _, sequence := range []string{"\x1b[A", "\x1b[B", "\x1b[1;5C", "\x1bOP", "\x1b[200~"} {
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
		out[i] = Item{Code: code}
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

// The whole readout is this one box: the keys typed, then what still
// matches. Nothing else on screen repeats it.
func TestRenderStatusShowsTypedAndCount(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.renderStatus(2, "a")
	term.Flush()
	rows := drawnRows(out.String())
	if len(rows) != 2 {
		t.Fatalf("want the echo and the count on their own rows, got %q", rows)
	}
	if rows[0] != "a" {
		t.Fatalf("first row = %q, want the typed prefix", rows[0])
	}
	if rows[1] != "2 links" {
		t.Fatalf("second row = %q, want the match count", rows[1])
	}

	// Before the first keystroke the echo row is blank, and the count has
	// not moved.
	term, out = keyTerminal(t, nil)
	term.renderStatus(3, "")
	term.Flush()
	if rows := drawnRows(out.String()); len(rows) != 1 || rows[0] != "3 links" {
		t.Fatalf("untyped pane = %q, want the count alone", rows)
	}
	if before, after := countRow(out.String()), countRow(typedOut(t)); before != after {
		t.Fatalf("the count moved from row %d to %d when typing started", before, after)
	}
}

func TestRenderStatusSaysWhenNothingMatches(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	term.renderStatus(0, "z")
	term.Flush()
	if rows := drawnRows(out.String()); rows[1] != "no match" {
		t.Fatalf("second row = %q, want no match", rows[1])
	}
}

func drawnRows(out string) []string {
	var rows []string
	for _, row := range strings.Split(strings.TrimPrefix(out, "\x1b[2J\x1b[H"), "\r\n") {
		if trimmed := strings.TrimSpace(stripSGR(row)); trimmed != "" {
			rows = append(rows, trimmed)
		}
	}
	return rows
}

func stripSGR(s string) string {
	var b strings.Builder
	for {
		before, rest, found := strings.Cut(s, "\x1b[")
		b.WriteString(before)
		if !found {
			return b.String()
		}
		_, s, _ = strings.Cut(rest, "m")
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

// OnNarrow skips the resolving keystroke.
func TestPickReportsEachNarrowing(t *testing.T) {
	t.Parallel()
	var typed []string
	var counts []int
	term, _ := keyTerminal(t, []byte("sa"))
	opts := Options{
		Alphabet: "asdfghjkl",
		OnNarrow: func(matches []int, prefix string) {
			typed = append(typed, prefix)
			counts = append(counts, len(matches))
		},
	}
	if _, ok := Pick(term, items("aa", "as", "ad", "sa", "ss"), opts); !ok {
		t.Fatal("Pick() did not select")
	}
	if want := []string{"", "s"}; !slices.Equal(typed, want) {
		t.Fatalf("OnNarrow saw %q, want %q", typed, want)
	}
	if want := []int{5, 2}; !slices.Equal(counts, want) {
		t.Fatalf("OnNarrow saw match counts %v, want %v", counts, want)
	}
}

func TestPickReportsNarrowingWithoutATerminal(t *testing.T) {
	t.Parallel()
	seen := 0
	term, _ := lineTerminal("ad\n")
	opts := Options{Alphabet: "asdfghjkl", OnNarrow: func([]int, string) { seen++ }}
	if _, ok := Pick(term, items("aa", "as", "ad"), opts); !ok {
		t.Fatal("Pick() did not select")
	}
	if seen != 1 {
		t.Fatalf("OnNarrow ran %d times, want 1", seen)
	}
}

// Not parallel: Theme caches under HOME, so each test gets a fresh one.
func themeEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
}

// A picker that cannot ask for the terminal's palette has to say so rather
// than guess.
func TestThemeReadsTheTerminalsColours(t *testing.T) {
	themeEnv(t)
	replies := "\x1b]10;rgb:cdcd/d6d6/f4f4\x1b\\" +
		"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\" +
		"\x1b]4;1;rgb:dcdc/3232/2f2f\x1b\\" +
		"\x1b]4;3;rgb:f9f9/e2e2/afaf\x1b\\" +
		"\x1b]4;4;rgb:2626/8b8b/d2d2\x1b\\"
	term, out := keyTerminal(t, []byte(replies))

	got := term.Theme("test-live-read")
	if got.Foreground != (color.RGBA{R: 0xCD, G: 0xD6, B: 0xF4, A: 0xFF}) {
		t.Fatalf("Foreground = %+v", got.Foreground)
	}
	if got.Background != (color.RGBA{R: 0x1E, G: 0x1E, B: 0x2E, A: 0xFF}) {
		t.Fatalf("Background = %+v", got.Background)
	}
	if got.AccentRed != (color.RGBA{R: 0xDC, G: 0x32, B: 0x2F, A: 0xFF}) {
		t.Fatalf("AccentRed = %+v", got.AccentRed)
	}
	if got.Accent != (color.RGBA{R: 0xF9, G: 0xE2, B: 0xAF, A: 0xFF}) {
		t.Fatalf("Accent = %+v", got.Accent)
	}
	if got.AccentBlue != (color.RGBA{R: 0x26, G: 0x8B, B: 0xD2, A: 0xFF}) {
		t.Fatalf("AccentBlue = %+v", got.AccentBlue)
	}
	term.Flush()
	for _, key := range theme.Keys {
		if !strings.Contains(out.String(), theme.Query(key)) {
			t.Fatalf("colour %q was never asked for: %q", key, out.String())
		}
	}
}

func TestThemeFallsBackWhenTheTerminalStaysQuiet(t *testing.T) {
	themeEnv(t)
	term, _ := keyTerminal(t, nil)
	if got := term.Theme("test-quiet"); got != theme.Fallback() {
		t.Fatalf("Theme() = %+v, want the fallback", got)
	}
	line, _ := lineTerminal("")
	if got := line.Theme("test-quiet"); got != theme.Fallback() {
		t.Fatalf("Theme() on a pipe = %+v, want the fallback", got)
	}
}

// A terminal that answers only some queries keeps the rest at fallback.
func TestThemeKeepsWhatDidArrive(t *testing.T) {
	themeEnv(t)
	term, _ := keyTerminal(t, []byte("\x1b]11;rgb:1e/1e/2e\a"))
	got := term.Theme("test-partial")
	if got.Background != (color.RGBA{R: 0x1E, G: 0x1E, B: 0x2E, A: 0xFF}) {
		t.Fatalf("Background = %+v", got.Background)
	}
	if got.Accent != theme.Fallback().Accent {
		t.Fatalf("Accent = %+v, want the fallback", got.Accent)
	}
	if got.AccentRed != theme.Fallback().AccentRed || got.AccentBlue != theme.Fallback().AccentBlue {
		t.Fatalf("alternates = %+v, %+v, want the fallback", got.AccentRed, got.AccentBlue)
	}
	if _, ok := theme.Load("test-partial"); ok {
		t.Fatal("Theme() saved a partial reply")
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

// A colour reply that misses the theme deadline used to close the picker
// and then type its payload.
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

// A terminal that answered before is not asked again: the cached reply
// comes back with nothing written to it.
func TestThemeUsesTheCache(t *testing.T) {
	themeEnv(t)
	want := theme.Colors{
		Foreground: color.RGBA{R: 0xCD, G: 0xD6, B: 0xF4, A: 0xFF},
		Background: color.RGBA{R: 0x1E, G: 0x1E, B: 0x2E, A: 0xFF},
		AccentRed:  color.RGBA{R: 0xDC, G: 0x32, B: 0x2F, A: 0xFF},
		Accent:     color.RGBA{R: 0xF9, G: 0xE2, B: 0xAF, A: 0xFF},
		AccentBlue: color.RGBA{R: 0x26, G: 0x8B, B: 0xD2, A: 0xFF},
	}
	if err := theme.Save("test-cached", want); err != nil {
		t.Fatal(err)
	}
	term, out := keyTerminal(t, nil)
	if got := term.Theme("test-cached"); got != want {
		t.Fatalf("Theme() = %+v, want the cached colours", got)
	}
	term.Flush()
	if out.Len() != 0 {
		t.Fatalf("Theme() asked the terminal: %q", out.String())
	}
}

// A full live reply is remembered for the next run; a partial one is not.
func TestThemeSavesAFullReply(t *testing.T) {
	themeEnv(t)
	replies := "\x1b]10;rgb:cdcd/d6d6/f4f4\x1b\\" +
		"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\" +
		"\x1b]4;1;rgb:dcdc/3232/2f2f\x1b\\" +
		"\x1b]4;3;rgb:f9f9/e2e2/afaf\x1b\\" +
		"\x1b]4;4;rgb:2626/8b8b/d2d2\x1b\\"
	term, _ := keyTerminal(t, []byte(replies))
	got := term.Theme("test-live")
	if _, ok := theme.Load("test-live"); !ok {
		t.Fatal("Theme() did not save the full reply")
	}
	if got.Background != (color.RGBA{R: 0x1E, G: 0x1E, B: 0x2E, A: 0xFF}) {
		t.Fatalf("Theme() = %+v", got)
	}
}

func typedOut(t *testing.T) string {
	t.Helper()
	term, out := keyTerminal(t, nil)
	term.renderStatus(2, "a")
	term.Flush()
	return out.String()
}

func countRow(out string) int {
	for i, row := range strings.Split(strings.TrimPrefix(out, "\x1b[2J\x1b[H"), "\r\n") {
		if strings.Contains(stripSGR(row), "link") {
			return i
		}
	}
	return -1
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

// The count is the line the eye goes to, so it takes the middle row and the
// echo sits above it.
func TestStatusPutsTheCountOnTheMiddleRow(t *testing.T) {
	t.Parallel()
	for _, rows := range []int{3, 5, 7} {
		term, out := keyTerminal(t, nil)
		term.rows = rows
		term.renderStatus(3, "")
		term.Flush()
		if got, want := countRow(out.String()), rows/2; got != want {
			t.Fatalf("in %d rows the count is on row %d, want the middle row %d", rows, got, want)
		}
	}
}
