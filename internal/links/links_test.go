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
		visible := []Visible{{Match: "https://a.io/x", Row: 0, Col: 4}}
		hidden := []ansi.Link{{URL: "https://a.io/x", Row: 0, Col: 4, Label: "https://a.io/x"}}
		if got := Merge(visible, hidden); len(got) != 1 || got[0].Kind != OSC8 {
			t.Fatalf("Merge() = %+v", got)
		}
	})

	t.Run("same url collapses even at a different cell", func(t *testing.T) {
		t.Parallel()
		visible := []Visible{{Match: "https://a.io/x", Row: 9, Col: 0}}
		hidden := []ansi.Link{{URL: "https://a.io/x", Row: 0, Col: 4, Label: "x"}}
		if got := Merge(visible, hidden); len(got) != 1 {
			t.Fatalf("Merge() = %+v, want the hidden link only", got)
		}
	})

	t.Run("hidden link keeps its anchor text", func(t *testing.T) {
		t.Parallel()
		got := Merge(nil, []ansi.Link{{URL: "https://github.com/o/r/pull/232", Row: 0, Col: 4, Label: "#232"}})
		if len(got) != 1 || got[0].Text != "#232" || got[0].URL != "https://github.com/o/r/pull/232" {
			t.Fatalf("Merge() = %+v", got)
		}
	})

	t.Run("bare host gets a scheme", func(t *testing.T) {
		t.Parallel()
		got := Merge([]Visible{{Match: "www.x.io/a", Row: 1, Col: 0}}, nil)
		if len(got) != 1 || got[0].URL != "https://www.x.io/a" || got[0].Text != "www.x.io/a" {
			t.Fatalf("Merge() = %+v", got)
		}
	})
}

func TestDedupe(t *testing.T) {
	t.Parallel()
	in := []Link{
		{URL: "https://a.io/x", Pane: "p1"},
		{URL: "https://a.io/x", Pane: "p2"},
		{URL: "https://b.io/y", Pane: "p2"},
	}
	got := Dedupe(in)
	if len(got) != 2 || got[0].Pane != "p1" || got[1].URL != "https://b.io/y" {
		t.Fatalf("Dedupe() = %+v", got)
	}
	if Dedupe(nil) != nil {
		t.Fatal("Dedupe(nil) should stay nil")
	}
}
