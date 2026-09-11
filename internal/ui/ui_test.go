package ui

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"
)

// keyTerminal drives a Terminal from a fixed byte stream, the way a real
// terminal would deliver keystrokes in raw mode.
func keyTerminal(t *testing.T, input []byte) (*Terminal, *bytes.Buffer) {
	t.Helper()
	keys := make(chan byte, len(input))
	for _, b := range input {
		keys <- b
	}
	close(keys)
	var out bytes.Buffer
	return &Terminal{out: bufio.NewWriter(&out), keys: keys, rows: fallbackRows}, &out
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
