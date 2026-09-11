package cells

import "testing"

func TestWidth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "https://example.com", 19},
		{"accented latin", "café", 4},
		{"box drawing", "├── src", 7},
		{"combining mark", "é", 1},
		{"cjk", "日本語", 6},
		{"hangul", "한글", 4},
		{"fullwidth", "ＡＢ", 4},
		{"emoji", "🚀", 2},
		{"emoji with variation selector", "❤️", 1},
		{"zero width joiner", "a\u200db", 2},
		{"mixed", "日本 ok", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Width(tc.in); got != tc.want {
				t.Fatalf("Width(%q) = %d; want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestColumn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		line  string
		index int
		want  int
	}{
		{"start", "see https://x.test", 0, 0},
		{"ascii prefix", "see https://x.test", 4, 4},
		// The bug this package exists for: a byte offset past multibyte text
		// is far larger than the column it names.
		{"cjk prefix", "日本語 https://x.test", 10, 7},
		{"emoji prefix", "🚀 https://x.test", 5, 3},
		{"past the end", "abc", 99, 3},
		{"negative", "abc", -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Column(tc.line, tc.index); got != tc.want {
				t.Fatalf("Column(%q, %d) = %d; want %d", tc.line, tc.index, got, tc.want)
			}
		})
	}
}

// A byte offset and a column agree only while the line stays ASCII.
func TestColumnMatchesByteOffsetForASCII(t *testing.T) {
	t.Parallel()
	line := "see https://example.com/docs for more"
	for i := range len(line) + 1 {
		if got := Column(line, i); got != i {
			t.Fatalf("Column(%q, %d) = %d; want %d", line, i, got, i)
		}
	}
}
