package scan

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/links"
)

// fakeSource answers pane reads from a table of raw ANSI snapshots.
// Plain text works as a snapshot: no escapes means nothing to strip.
type fakeSource struct {
	raw     map[string]string
	readErr error
}

func (f *fakeSource) PaneANSI(_ context.Context, pane string) (string, error) {
	if f.readErr != nil {
		return "", f.readErr
	}
	return f.raw[pane], nil
}

// panes sizes every pane the same: only the unwrap cares, and the cases
// that do not exercise it do not care what the number is.
func panes(ids ...string) []Pane {
	out := make([]Pane, len(ids))
	for i, id := range ids {
		out[i] = Pane{ID: id, Cols: 100, Rows: 40}
	}
	return out
}

func newScanner(source Source) *Scanner {
	return &Scanner{Source: source, Log: slog.New(slog.DiscardHandler)}
}

func paneRaw(pane string, lines ...string) map[string]string {
	return map[string]string{
		pane: strings.Join(lines, "\n"),
	}
}

// One destination on two panes gets a hint on each.
func TestLinksHintsEveryCopyAcrossPanes(t *testing.T) {
	t.Parallel()
	source := &fakeSource{raw: map[string]string{}}
	for key, value := range paneRaw("w1:p1", "go https://a.io/x") {
		source.raw[key] = value
	}
	for key, value := range paneRaw("w1:p2", "again https://a.io/x plus https://b.io/y") {
		source.raw[key] = value
	}

	got := newScanner(source).Links(context.Background(), panes("w1:p1", "w1:p2"))
	var pairs [][2]string
	for _, l := range got {
		pairs = append(pairs, [2]string{l.URL, l.Pane})
	}
	want := [][2]string{
		{"https://a.io/x", "w1:p1"},
		{"https://a.io/x", "w1:p2"},
		{"https://b.io/y", "w1:p2"},
	}
	if !reflect.DeepEqual(pairs, want) {
		t.Fatalf("Links() = %+v, want %+v", pairs, want)
	}
}

// One snapshot carries both: the URL on screen and the one hiding
// behind styled anchor text arrive on the same read.
func TestLinksReadsTextAndOSC8FromOneSnapshot(t *testing.T) {
	t.Parallel()
	source := &fakeSource{raw: paneRaw("w1:p1",
		"\x1b[1mgo\x1b[0m https://a.io/x here",
		"see \x1b]8;;https://g.io/pull/232\x1b\\#232\x1b]8;;\x1b\\ merged",
	)}
	got := newScanner(source).Links(context.Background(), panes("w1:p1"))
	var urls []string
	for _, l := range got {
		urls = append(urls, l.URL)
	}
	want := []string{"https://g.io/pull/232", "https://a.io/x"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("Links() = %+v, want %+v", urls, want)
	}
}

func TestLinksSurvivesAFailedPane(t *testing.T) {
	t.Parallel()
	source := &fakeSource{readErr: errors.New("pane is gone")}
	if got := newScanner(source).Links(context.Background(), panes("w1:p1")); got != nil {
		t.Fatalf("Links() = %+v, want nothing", got)
	}
}

func TestLocate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		visible []string
		choice  links.Link
		shift   int
		wantRow int
		wantCol int
		wantOK  bool
	}{
		{
			name:    "confirms a text link at the shifted row",
			visible: []string{"", "", "", "", "", "", "", "the https://a.io/x here"},
			choice:  links.Link{URL: "https://a.io/x", Text: "https://a.io/x", Kind: links.Text, Row: 10, Col: 4},
			shift:   3,
			wantRow: 7, wantCol: 4, wantOK: true,
		},
		{
			name:    "refuses the shifted row when another link took it",
			visible: []string{"", "", "", "", "", "", "", "the https://b.io/y here"},
			choice:  links.Link{URL: "https://a.io/x", Text: "https://a.io/x", Kind: links.Text, Row: 10, Col: 4},
			shift:   3,
		},
		{
			name:    "confirms a soft-wrapped url by the run that reaches the edge",
			visible: []string{"go https://a.io/long-ur", "l-continued here"},
			choice:  links.Link{URL: "https://a.io/long-url-continued", Text: "https://a.io/long-url-continued", Kind: links.Text, Row: 3, Col: 3},
			shift:   3,
			wantRow: 0, wantCol: 3, wantOK: true,
		},
		{
			name:    "confirms a soft-wrapped url that has not scrolled",
			visible: []string{"go https://a.io/long-ur", "l-continued here"},
			choice:  links.Link{URL: "https://a.io/long-url-continued", Text: "https://a.io/long-url-continued", Kind: links.Text, Row: 0, Col: 3},
			wantRow: 0, wantCol: 3, wantOK: true,
		},
		{
			name:    "confirms a soft-wrapped url past wide characters",
			visible: []string{"日https://a.io/long-ur", "l-continued here"},
			choice:  links.Link{URL: "https://a.io/long-url-continued", Text: "https://a.io/long-url-continued", Kind: links.Text, Row: 0, Col: 2},
			wantRow: 0, wantCol: 2, wantOK: true,
		},
		{
			name:    "confirms a soft-wrapped url past a zero-width rune",
			visible: []string{"a\u200dhttps://a.io/long-ur", "l-continued here"},
			choice:  links.Link{URL: "https://a.io/long-url-continued", Text: "https://a.io/long-url-continued", Kind: links.Text, Row: 0, Col: 1},
			wantRow: 0, wantCol: 1, wantOK: true,
		},
		{
			name:    "refuses a run with no continuation under it",
			visible: []string{"go https://a.io/long-ur"},
			choice:  links.Link{URL: "https://a.io/long-url-continued", Text: "https://a.io/long-url-continued", Kind: links.Text, Row: 0, Col: 3},
		},
		{
			name:    "refuses a longer url that merely contains the pick",
			visible: []string{"see https://a.io/xyz here"},
			choice:  links.Link{URL: "https://a.io/x", Text: "https://a.io/x", Kind: links.Text, Row: 0, Col: 4},
		},
		{
			name:    "refuses an anchor that is only a prefix of the one on screen",
			visible: []string{"see #2"},
			choice:  links.Link{URL: "https://g.io/pull/22", Text: "#22", Kind: links.OSC8, Row: 0, Col: 4},
		},
		{
			name:    "re-finds a text link that scrolled past the shift",
			visible: []string{"go https://a.io/x here"},
			choice:  links.Link{URL: "https://a.io/x", Text: "https://a.io/x", Kind: links.Text, Row: 1, Col: 4},
			shift:   5,
			wantRow: 0, wantCol: 3, wantOK: true,
		},
		{
			name:    "finds a hidden link by its anchor text",
			visible: []string{"see #2 merged"},
			choice:  links.Link{URL: "https://g.io/pull/2", Text: "#2", Kind: links.OSC8, Row: 9, Col: 9},
			wantRow: 0, wantCol: 4, wantOK: true,
		},
		{
			name:    "re-finds a text link past wide characters",
			visible: []string{"日本語 https://a.io/x"},
			choice:  links.Link{URL: "https://a.io/x", Text: "https://a.io/x", Kind: links.Text, Row: 4, Col: 0},
			shift:   9,
			wantRow: 0, wantCol: 7, wantOK: true,
		},
		{
			name:    "finds a hidden link past wide characters",
			visible: []string{"🚀 #2 merged"},
			choice:  links.Link{URL: "https://g.io/pull/2", Text: "#2", Kind: links.OSC8, Row: 9, Col: 9},
			wantRow: 0, wantCol: 3, wantOK: true,
		},
		{
			name:    "gives up when the link is gone",
			visible: []string{"nothing here"},
			choice:  links.Link{URL: "https://a.io/x", Text: "https://a.io/x", Kind: links.Text, Row: 0, Col: 4},
			shift:   5,
		},
		{
			name:    "gives up when a text link is gone without scrolling",
			visible: []string{"nothing here"},
			choice:  links.Link{URL: "https://a.io/x", Text: "https://a.io/x", Kind: links.Text, Row: 0, Col: 4},
			shift:   0,
		},
		{
			name:    "gives up when a scrolled link is nowhere to be found",
			visible: []string{"nothing here"},
			choice:  links.Link{URL: "https://g.io/pull/2", Text: "#2", Kind: links.OSC8, Row: 10, Col: 4},
			shift:   3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := &fakeSource{raw: map[string]string{"w1:p1": strings.Join(tc.visible, "\n")}}
			tc.choice.Pane = "w1:p1"
			row, col, ok := newScanner(source).Locate(context.Background(), tc.choice, tc.shift)
			if ok != tc.wantOK || (ok && (row != tc.wantRow || col != tc.wantCol)) {
				t.Fatalf("Locate() = %d, %d, %v; want %d, %d, %v", row, col, ok, tc.wantRow, tc.wantCol, tc.wantOK)
			}
		})
	}
}

// A line filled to the pane's last cell continues into the next one, and
// that is enough to finish the URL without asking Herdr for scrollback.
func TestLinksCompletesWrappedURL(t *testing.T) {
	t.Parallel()
	visible := []string{"go https://a.io/long-ur", "l-continued here"}
	source := &fakeSource{raw: paneRaw("w1:p1", visible...)}
	scanner := newScanner(source)
	got := scanner.Links(context.Background(), []Pane{{ID: "w1:p1", Cols: len(visible[0]), Rows: 24}})
	if len(got) != 1 || got[0].URL != "https://a.io/long-url-continued" {
		t.Fatalf("Links() = %+v", got)
	}
}

// A line that stops short of the edge ended there, so the next line is not
// a continuation however much it looks like one.
func TestLinksLeavesAShortLineAlone(t *testing.T) {
	t.Parallel()
	visible := []string{"go https://a.io/long-ur", "l-continued here"}
	source := &fakeSource{raw: paneRaw("w1:p1", visible...)}
	scanner := newScanner(source)
	got := scanner.Links(context.Background(), []Pane{{ID: "w1:p1", Cols: 100, Rows: 24}})
	if len(got) != 1 || got[0].URL != "https://a.io/long-ur" {
		t.Fatalf("Links() = %+v", got)
	}
}

// A trailing dot at the edge is a guess about prose versus URL, and the
// joined text built from that guess must not confirm it.
func TestLinksLeavesAWrappedDotAlone(t *testing.T) {
	t.Parallel()
	visible := []string{"see https://docs.a.io/guide/v2.", "1/install here"}
	source := &fakeSource{raw: paneRaw("w1:p1", visible...)}
	scanner := newScanner(source)
	got := scanner.Links(context.Background(), []Pane{{ID: "w1:p1", Cols: len(visible[0]), Rows: 24}})
	if len(got) != 1 || got[0].URL != "https://docs.a.io/guide/v2" {
		t.Fatalf("Links() = %+v", got)
	}
}

// The dot guard is per carry: a structural wrap on another line still
// completes from the joined text.
func TestLinksGuardsOnlyTheDotGuess(t *testing.T) {
	t.Parallel()
	visible := []string{
		"go visit! https://a.io/long-ur",
		"l-continued here",
		"see now! https://docs.a.io/v2.",
		"1/install here",
	}
	source := &fakeSource{raw: paneRaw("w1:p1", visible...)}
	scanner := newScanner(source)
	got := scanner.Links(context.Background(), []Pane{{ID: "w1:p1", Cols: len(visible[0]), Rows: 24}})
	var urls []string
	for _, l := range got {
		urls = append(urls, l.URL)
	}
	want := []string{"https://a.io/long-url-continued", "https://docs.a.io/v2"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("Links() = %+v, want %+v", urls, want)
	}
}

// A short dot line never joined by unwrap must not nuke a structural
// completion sharing its prefix, and the dot itself still stays Clean.
func TestLinksShortDotKeepsStructuralCompletion(t *testing.T) {
	t.Parallel()
	visible := []string{
		"hi https://a.io/v2.",
		"go https://a.io/v2.1-long-ur",
		"l-continued here",
	}
	source := &fakeSource{raw: paneRaw("w1:p1", visible...)}
	scanner := newScanner(source)
	got := scanner.Links(context.Background(), []Pane{{ID: "w1:p1", Cols: len(visible[1]), Rows: 24}})
	var urls []string
	for _, l := range got {
		urls = append(urls, l.URL)
	}
	want := []string{"https://a.io/v2", "https://a.io/v2.1-long-url-continued"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("Links() = %+v, want %+v", urls, want)
	}
}

func TestLinksNeverReadsScrollback(t *testing.T) {
	t.Parallel()
	source := &fakeSource{raw: paneRaw("w1:p1", "go https://a.io/x here")}
	scanner := newScanner(source)
	got := scanner.Links(context.Background(), panes("w1:p1"))
	if len(got) != 1 || got[0].URL != "https://a.io/x" {
		t.Fatalf("Links() = %+v", got)
	}
}

func TestCarriesFindsWhatNeedsUnwrapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		visible []string
		want    bool
	}{
		{name: "no urls", visible: []string{"nothing here", "either"}, want: false},
		{name: "complete url mid-line", visible: []string{"go https://a.io/x here", "next"}, want: false},
		{name: "complete url at end of last line", visible: []string{"go https://a.io/x"}, want: false},
		{name: "trailing dot at edge is ambiguous", visible: []string{"see https://a.io/x.", "next"}, want: true},
		{name: "url wrapped across lines", visible: []string{"go https://a.io/long-ur", "l-continued"}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := len(carries(tc.visible, links.MatchLines(tc.visible))) > 0
			if got != tc.want {
				t.Fatalf("carries(%q) = %v, want %v", tc.visible, got, tc.want)
			}
		})
	}
}
