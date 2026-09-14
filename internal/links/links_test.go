package links

import (
	"reflect"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/ansi"
)

func TestClean(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"https://x.io/a).", "https://x.io/a"},
		{"https://x.io/a,", "https://x.io/a"},
		{"https://x.io/a?b=1#c", "https://x.io/a?b=1#c"},
		{"https://x.io/Foo_(bar)", "https://x.io/Foo_(bar)"},
		{"https://x.io/Foo_(bar).", "https://x.io/Foo_(bar)"},
		{"https://x.io/a]", "https://x.io/a"},
		{"https://x.io/a[b]", "https://x.io/a[b]"},
	}
	for _, tc := range tests {
		if got := Clean(tc.in); got != tc.want {
			t.Errorf("Clean(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	if got := Normalize("www.x.io/a"); got != "https://www.x.io/a" {
		t.Errorf("Normalize() = %q", got)
	}
	if got := Normalize("https://x.io"); got != "https://x.io" {
		t.Errorf("Normalize() = %q", got)
	}
}

func TestFindAllSchemes(t *testing.T) {
	t.Parallel()
	line := "mail me at mailto:a@b.io or see www.x.io/docs and file:///tmp/x"
	var got []string
	for _, m := range FindAll(line) {
		got = append(got, m.URL)
	}
	want := []string{"mailto:a@b.io", "www.x.io/docs", "file:///tmp/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindAll() = %+v, want %+v", got, want)
	}
}

func TestFromLines(t *testing.T) {
	t.Parallel()
	fragment := "https://a.io/very/long/pa"
	tests := []struct {
		name  string
		lines []string
		known map[string]bool
		want  []Visible
	}{
		{
			name:  "one per line",
			lines: []string{"see https://a.io/x here", "no link", "go https://b.io/y!"},
			want:  []Visible{{Match: "https://a.io/x", Row: 0, Col: 4}, {Match: "https://b.io/y", Row: 2, Col: 3}},
		},
		{
			name:  "wrapped url completed from scrollback",
			lines: []string{"start " + fragment, "th/continued done", "tail"},
			known: map[string]bool{fragment + "th/continued": true},
			want:  []Visible{{Match: fragment + "th/continued", Row: 0, Col: 6}},
		},
		{
			name:  "no false join when scrollback disagrees",
			lines: []string{"see https://a.io/x", "done"},
			want:  []Visible{{Match: "https://a.io/x", Row: 0, Col: 4}},
		},
		{
			name:  "blank next line",
			lines: []string{"see https://a.io/x", ""},
			want:  []Visible{{Match: "https://a.io/x", Row: 0, Col: 4}},
		},
		{
			name:  "url on the last line is never carried",
			lines: []string{"see https://a.io/x"},
			want:  []Visible{{Match: "https://a.io/x", Row: 0, Col: 4}},
		},
		{
			name:  "continuation does not hide a later url",
			lines: []string{"start " + fragment, "th/continued then https://b.io/y"},
			known: map[string]bool{fragment + "th/continued": true},
			want: []Visible{
				{Match: fragment + "th/continued", Row: 0, Col: 6},
				{Match: "https://b.io/y", Row: 1, Col: 18},
			},
		},
		{
			name:  "trailing dot never completes from known",
			lines: []string{"see https://docs.a.io/guide/v2.", "1/install here"},
			known: map[string]bool{"https://docs.a.io/guide/v2.1/install": true},
			want:  []Visible{{Match: "https://docs.a.io/guide/v2", Row: 0, Col: 4}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			known := tc.known
			if known == nil {
				known = map[string]bool{}
			}
			if got := FromLines(tc.lines, known); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("FromLines() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestKnown(t *testing.T) {
	t.Parallel()
	known := Known("see https://a.io/x and https://b.io/y.")
	if !known["https://a.io/x"] || !known["https://b.io/y"] {
		t.Fatalf("Known() = %+v", known)
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()
	t.Run("same cell collapses", func(t *testing.T) {
		t.Parallel()
		lines := []string{"see https://a.io/x here"}
		visible := []Visible{{Match: "https://a.io/x", Row: 0, Col: 4}}
		hidden := []ansi.Link{{URL: "https://a.io/x", Row: 0, Col: 4, Label: "https://a.io/x"}}
		if got := Merge(lines, visible, hidden); len(got) != 1 || got[0].Kind != OSC8 {
			t.Fatalf("Merge() = %+v", got)
		}
	})

	// The same destination in two places gets two hints now: a hint has to
	// be on the copy you are looking at.
	t.Run("a repeated anchor is marked everywhere", func(t *testing.T) {
		t.Parallel()
		lines := []string{
			"yes, #971 is still a draft",
			"",
			"auto mode on \u00b7 PR #971 \u00b7 1 agent",
		}
		hidden := []ansi.Link{{URL: "https://g.io/pull/971", Row: 0, Col: 5, Label: "#971"}}
		got := Merge(lines, nil, hidden)
		if len(got) != 2 {
			t.Fatalf("Merge() = %+v, want both occurrences", got)
		}
		if got[0].Row != 0 || got[0].Col != 5 {
			t.Fatalf("Merge()[0] = %+v, want row 0 col 5", got[0])
		}
		if got[1].Row != 2 || got[1].Col != 18 {
			t.Fatalf("Merge()[1] = %+v, want row 2 col 18", got[1])
		}
	})

	// A replay smaller than the pane puts a link off the bottom of the
	// screen, so the anchor search is what actually places it.
	t.Run("the anchor beats the replay position", func(t *testing.T) {
		t.Parallel()
		lines := []string{"", "", "", "the #232 pull request"}
		hidden := []ansi.Link{{URL: "https://g.io/pull/232", Row: 0, Col: 0, Label: "#232"}}
		got := Merge(lines, nil, hidden)
		if len(got) != 1 || got[0].Row != 3 || got[0].Col != 4 {
			t.Fatalf("Merge() = %+v, want row 3 col 4", got)
		}
	})

	t.Run("an anchor nowhere on screen keeps the replay position", func(t *testing.T) {
		t.Parallel()
		got := Merge([]string{"nothing here"}, nil, []ansi.Link{{URL: "https://g.io/pull/232", Row: 7, Col: 4, Label: "#232"}})
		if len(got) != 1 || got[0].Row != 7 || got[0].Col != 4 {
			t.Fatalf("Merge() = %+v, want row 7 col 4", got)
		}
	})

	// One visual link seen before and after output arrived mid-scan is
	// not two links: the stale replay yields to the snapshot placement
	// next to it, whatever order the stream delivered them in.
	t.Run("a stale replay beside a placed link is dropped", func(t *testing.T) {
		t.Parallel()
		lines := []string{
			"Pushed. Now the PR description.",
			"Edited PR #971",
		}
		hidden := []ansi.Link{
			{URL: "https://g.io/pull/971", Row: 0, Col: 10, Label: "pull 971"},
			{URL: "https://g.io/pull/971", Row: 1, Col: 10, Label: "#971"},
		}
		got := Merge(lines, nil, hidden)
		if len(got) != 1 || got[0].Row != 1 || got[0].Col != 10 || got[0].Text != "#971" {
			t.Fatalf("Merge() = %+v, want the single snapshot placement", got)
		}
	})

	t.Run("a stale replay beside a visible link is dropped", func(t *testing.T) {
		t.Parallel()
		lines := []string{"", "", "", "", "", "see https://a.io/x here"}
		visible := []Visible{{Match: "https://a.io/x", Row: 5, Col: 4}}
		hidden := []ansi.Link{{URL: "https://a.io/x", Row: 4, Col: 4, Label: "the link"}}
		got := Merge(lines, visible, hidden)
		if len(got) != 1 || got[0].Kind != Text || got[0].Row != 5 {
			t.Fatalf("Merge() = %+v, want the single visible link", got)
		}
	})

	t.Run("a replay far from a placed link is kept", func(t *testing.T) {
		t.Parallel()
		lines := []string{"see #2 here", "", "", "", "", "", "", "", "", "", "nothing here"}
		hidden := []ansi.Link{
			{URL: "https://g.io/pull/2", Row: 0, Col: 4, Label: "#2"},
			{URL: "https://g.io/pull/2", Row: 10, Col: 0, Label: "second"},
		}
		got := Merge(lines, nil, hidden)
		if len(got) != 2 {
			t.Fatalf("Merge() = %+v, want both placements", got)
		}
	})

	t.Run("hidden link keeps its anchor text", func(t *testing.T) {
		t.Parallel()
		got := Merge(nil, nil, []ansi.Link{{URL: "https://github.com/o/r/pull/232", Row: 0, Col: 4, Label: "#232"}})
		if len(got) != 1 || got[0].Text != "#232" || got[0].URL != "https://github.com/o/r/pull/232" {
			t.Fatalf("Merge() = %+v", got)
		}
	})

	t.Run("bare host gets a scheme", func(t *testing.T) {
		t.Parallel()
		got := Merge([]string{"www.x.io/a"}, []Visible{{Match: "www.x.io/a", Row: 1, Col: 0}}, nil)
		if len(got) != 1 || got[0].URL != "https://www.x.io/a" || got[0].Text != "www.x.io/a" {
			t.Fatalf("Merge() = %+v", got)
		}
	})

	t.Run("a flood of matches is capped", func(t *testing.T) {
		t.Parallel()
		lines := make([]string, 40)
		for i := range lines {
			lines[i] = "see #2 here"
		}
		got := Merge(lines, nil, []ansi.Link{{URL: "https://g.io/pull/2", Label: "#2"}})
		if len(got) != maxAnchorHits {
			t.Fatalf("Merge() marked %d cells, want %d", len(got), maxAnchorHits)
		}
	})
}

func TestMergeCountsBlanksBeforeALink(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		col  int
		want int
	}{
		{"room in the gutter", "see    https://a.io/x", 7, 4},
		{"flush against a word", "(https://a.io/x)", 1, 0},
		{"start of the line", "https://a.io/x", 0, 0},
		{"wide characters do not count as blanks", "\u65e5\u672c https://a.io/x", 5, 1},
		{"past the end of the text", "ab", 6, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Merge([]string{tc.line}, []Visible{{Match: "https://a.io/x", Row: 0, Col: tc.col}}, nil)
			if len(got) != 1 {
				t.Fatalf("Merge() = %+v", got)
			}
			if got[0].Before != tc.want {
				t.Fatalf("Before = %d, want %d", got[0].Before, tc.want)
			}
		})
	}
}

// A column is a screen cell, not a byte offset: a byte offset lands far to
// the right on any line with wide text.
func TestFromLinesReportsDisplayColumns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want int
	}{
		{"ascii", "see https://a.io/x", 4},
		{"accented latin", "café https://a.io/x", 5},
		{"cjk", "日本語 https://a.io/x", 7},
		{"emoji", "🚀 https://a.io/x", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FromLines([]string{tc.line}, nil)
			if len(got) != 1 {
				t.Fatalf("FromLines(%q) = %+v, want one link", tc.line, got)
			}
			if got[0].Col != tc.want {
				t.Fatalf("FromLines(%q) col = %d, want %d", tc.line, got[0].Col, tc.want)
			}
		})
	}
}

func TestMergeSkipsAnchorInsideLongerText(t *testing.T) {
	t.Parallel()
	lines := []string{"#1 opened, #12 merged, #123 closed"}
	hidden := []ansi.Link{{URL: "https://g.io/pull/1", Row: 0, Col: 0, Label: "#1"}}
	got := Merge(lines, nil, hidden)
	if len(got) != 1 || got[0].Row != 0 || got[0].Col != 0 {
		t.Fatalf("Merge() = %+v, want only the standalone #1", got)
	}
}
