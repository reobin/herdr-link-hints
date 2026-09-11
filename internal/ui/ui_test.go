package ui

import (
	"bufio"
	"bytes"
	"image/color"
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
		out[i] = Item{Code: code, Text: "https://x.io/" + code, Row: i}
	}
	return out
}

func TestPick(t *testing.T) {
	t.Parallel()
	opts := Options{Title: "pane neon", Alphabet: "asdfghjkl"}
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

func TestRenderTruncatesToTheScreen(t *testing.T) {
	t.Parallel()
	long := make([]string, 40)
	for i := range long {
		long[i] = string(rune('a'+i%9)) + string(rune('a'+i/9))
	}
	term, out := keyTerminal(t, nil)
	list := items(long...)
	term.render(list, indices(list), "", Options{Title: "pane neon", Alphabet: "asdfghjkl"})
	term.Flush()

	text := out.String()
	if !strings.Contains(text, "more, keep typing to narrow") {
		t.Fatalf("expected an overflow note, got:\n%s", text)
	}
	if lines := strings.Count(text, "\r\n"); lines > fallbackRows {
		t.Fatalf("rendered %d lines into a %d-row screen", lines, fallbackRows)
	}
}

func TestRenderShowsPaneNames(t *testing.T) {
	t.Parallel()
	term, out := keyTerminal(t, nil)
	list := []Item{{Code: "a", Text: "https://x.io", Where: "neon", Row: 4}}
	term.render(list, indices(list), "", Options{Title: "2 panes", Alphabet: "asdfghjkl"})
	term.Flush()
	if !strings.Contains(out.String(), "[neon r5]") {
		t.Fatalf("expected the pane name and 1-based row, got:\n%s", out.String())
	}
}

// The annotate popup holds nothing but this line, so it is centred on what
// the reader sees and on the rows the pane actually got.
func TestRenderStatusCentresTheCount(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		rows      int
		wantBlank int
	}{
		{"one row has nowhere to centre", 1, 0},
		{"an even pane leans up", 2, 0},
		{"an odd pane centres", 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			term, out := keyTerminal(t, nil)
			term.rows = tc.rows
			list := items("aa", "as", "ad")
			term.render(list, indices(list)[:2], "a", Options{Alphabet: "asdfghjkl", Style: StyleStatus})
			term.Flush()

			shown := status(2)
			drawn := strings.TrimPrefix(out.String(), "\x1b[2J\x1b[H")
			if blank := strings.Count(drawn, "\r\n"); blank != tc.wantBlank {
				t.Fatalf("status sits under %d blank rows, want %d: %q", blank, tc.wantBlank, drawn)
			}
			if !strings.HasSuffix(drawn, shown.String()) {
				t.Fatalf("the status should count the matches, got:\n%q", drawn)
			}
			drawn = strings.TrimLeft(drawn, "\r\n")
			lead := len(drawn) - len(strings.TrimLeft(drawn, " "))
			if want := centrePad(fallbackCols, shown.width()); lead != want {
				t.Fatalf("status starts at column %d, want %d", lead, want)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		matches int
		want    string
	}{{0, "no match"}, {1, "1 link"}, {12, "12 links"}}
	for _, tc := range cases {
		got := status(tc.matches)
		if plain(got) != tc.want {
			t.Fatalf("status(%d) = %q, want %q", tc.matches, plain(got), tc.want)
		}
		if got.width() != len(tc.want) {
			t.Fatalf("status(%d) is %d wide, want %d", tc.matches, got.width(), len(tc.want))
		}
	}
	// The count carries the weight and its unit recedes.
	if counted := status(12); counted[0].sgr == counted[1].sgr {
		t.Fatalf("status() = %+v, want the count and its unit styled apart", counted)
	}
	if status(0)[0].sgr == status(12)[0].sgr {
		t.Fatal("a ruled-out prefix should not look like a count")
	}
}

func plain(l line) string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.text)
	}
	return b.String()
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

// OnNarrow has to fire once before the first keystroke and again on every
// change.
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
	if want := []string{"", "s", "sa"}; !slices.Equal(typed, want) {
		t.Fatalf("OnNarrow saw %q, want %q", typed, want)
	}
	if want := []int{5, 2, 1}; !slices.Equal(counts, want) {
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

// A picker that cannot ask for the terminal's palette has to say so rather
// than guess.
func TestThemeReadsTheTerminalsColours(t *testing.T) {
	t.Parallel()
	replies := "\x1b]10;rgb:cdcd/d6d6/f4f4\x1b\\" +
		"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\" +
		"\x1b]4;3;rgb:f9f9/e2e2/afaf\x1b\\"
	term, out := keyTerminal(t, []byte(replies))

	got := term.Theme()
	if got.Foreground != (color.RGBA{R: 0xCD, G: 0xD6, B: 0xF4, A: 0xFF}) {
		t.Fatalf("Foreground = %+v", got.Foreground)
	}
	if got.Background != (color.RGBA{R: 0x1E, G: 0x1E, B: 0x2E, A: 0xFF}) {
		t.Fatalf("Background = %+v", got.Background)
	}
	if got.Accent != (color.RGBA{R: 0xF9, G: 0xE2, B: 0xAF, A: 0xFF}) {
		t.Fatalf("Accent = %+v", got.Accent)
	}
	term.Flush()
	for _, key := range theme.Keys {
		if !strings.Contains(out.String(), theme.Query(key)) {
			t.Fatalf("colour %q was never asked for: %q", key, out.String())
		}
	}
}

func TestThemeFallsBackWhenTheTerminalStaysQuiet(t *testing.T) {
	t.Parallel()
	term, _ := keyTerminal(t, nil)
	if got := term.Theme(); got != theme.Fallback() {
		t.Fatalf("Theme() = %+v, want the fallback", got)
	}
	line, _ := lineTerminal("")
	if got := line.Theme(); got != theme.Fallback() {
		t.Fatalf("Theme() on a pipe = %+v, want the fallback", got)
	}
}

// A terminal that answers only some queries keeps the rest at fallback.
func TestThemeKeepsWhatDidArrive(t *testing.T) {
	t.Parallel()
	term, _ := keyTerminal(t, []byte("\x1b]11;rgb:1e/1e/2e\a"))
	got := term.Theme()
	if got.Background != (color.RGBA{R: 0x1E, G: 0x1E, B: 0x2E, A: 0xFF}) {
		t.Fatalf("Background = %+v", got.Background)
	}
	if got.Accent != theme.Fallback().Accent {
		t.Fatalf("Accent = %+v, want the fallback", got.Accent)
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
