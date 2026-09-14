package scan

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/ansi"
	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
)

// fakeSource answers pane reads from a table. A Scanner reads panes
// concurrently, so the counters are guarded.
type fakeSource struct {
	text    map[[2]string][]string
	hidden  map[string][]ansi.Link
	readErr error

	mu       sync.Mutex
	observed map[string]int
	sizes    map[string][2]int
	reads    map[[2]string]int
}

func (f *fakeSource) PaneLines(_ context.Context, pane, source string, _ int) ([]string, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reads == nil {
		f.reads = map[[2]string]int{}
	}
	f.reads[[2]string{pane, source}]++
	return f.text[[2]string{pane, source}], nil
}

func (f *fakeSource) ObserveOSC8(_ context.Context, pane string, cols, rows int) ([]ansi.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.observed == nil {
		f.observed = map[string]int{}
	}
	if f.sizes == nil {
		f.sizes = map[string][2]int{}
	}
	f.observed[pane]++
	f.sizes[pane] = [2]int{cols, rows}
	return f.hidden[pane], nil
}

func (f *fakeSource) observedSize(pane string) [2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sizes[pane]
}

func (f *fakeSource) observeCount(pane string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.observed[pane]
}

func (f *fakeSource) readCount(pane, source string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[[2]string{pane, source}]
}

// panes sizes every pane the same: only the observe call cares, and the
// cases that do not exercise it do not care what the number is.
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

func paneText(pane string, lines ...string) map[[2]string][]string {
	return map[[2]string][]string{
		{pane, herdr.SourceVisible}:   lines,
		{pane, herdr.SourceUnwrapped}: lines,
	}
}

// One destination on two panes gets a hint on each.
func TestLinksHintsEveryCopyAcrossPanes(t *testing.T) {
	t.Parallel()
	source := &fakeSource{text: map[[2]string][]string{}}
	for key, value := range paneText("w1:p1", "go https://a.io/x") {
		source.text[key] = value
	}
	for key, value := range paneText("w1:p2", "again https://a.io/x plus https://b.io/y") {
		source.text[key] = value
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

// A stream rendered at the wrong width wraps somewhere else, which moves
// every link it reports.
func TestLinksObservesAtThePaneSize(t *testing.T) {
	t.Parallel()
	source := &fakeSource{text: paneText("w1:p1", "nothing here")}
	newScanner(source).Links(context.Background(), []Pane{{ID: "w1:p1", Cols: 204, Rows: 57}})
	if got, want := source.observedSize("w1:p1"), [2]int{204, 57}; got != want {
		t.Fatalf("observed at %v, want %v", got, want)
	}
}

func TestLinksIncludesHiddenTargets(t *testing.T) {
	t.Parallel()
	source := &fakeSource{
		text:   paneText("w1:p1", "see #232 merged"),
		hidden: map[string][]ansi.Link{"w1:p1": {{URL: "https://g.io/pull/232", Row: 0, Col: 4, Label: "#232"}}},
	}
	got := newScanner(source).Links(context.Background(), panes("w1:p1"))
	if len(got) != 1 || got[0].URL != "https://g.io/pull/232" || got[0].Text != "#232" {
		t.Fatalf("Links() = %+v", got)
	}
}

func TestLinksSkipObserve(t *testing.T) {
	t.Parallel()
	source := &fakeSource{
		text:   paneText("w1:p1", "see #232 merged"),
		hidden: map[string][]ansi.Link{"w1:p1": {{URL: "https://g.io/pull/232", Label: "#232"}}},
	}
	scanner := newScanner(source)
	scanner.SkipObserve = true
	if got := scanner.Links(context.Background(), panes("w1:p1")); got != nil {
		t.Fatalf("Links() = %+v, want nothing without observe", got)
	}
	if source.observeCount("w1:p1") != 0 {
		t.Fatal("SkipObserve should not open an observe stream")
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
			name:    "shift is enough for a text link",
			choice:  links.Link{URL: "https://a.io/x", Text: "https://a.io/x", Kind: links.Text, Row: 10, Col: 4},
			shift:   3,
			wantRow: 7, wantCol: 4, wantOK: true,
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
			source := &fakeSource{text: map[[2]string][]string{{"w1:p1", herdr.SourceVisible}: tc.visible}}
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
	source := &fakeSource{text: map[[2]string][]string{{"w1:p1", herdr.SourceVisible}: visible}}
	scanner := newScanner(source)
	scanner.SkipObserve = true
	got := scanner.Links(context.Background(), []Pane{{ID: "w1:p1", Cols: len(visible[0]), Rows: 24}})
	if len(got) != 1 || got[0].URL != "https://a.io/long-url-continued" {
		t.Fatalf("Links() = %+v", got)
	}
	if n := source.readCount("w1:p1", herdr.SourceUnwrapped); n != 0 {
		t.Fatalf("unwrapped reads = %d, want none: the read scrolls the pane", n)
	}
}

// A line that stops short of the edge ended there, so the next line is not
// a continuation however much it looks like one.
func TestLinksLeavesAShortLineAlone(t *testing.T) {
	t.Parallel()
	visible := []string{"go https://a.io/long-ur", "l-continued here"}
	source := &fakeSource{text: map[[2]string][]string{{"w1:p1", herdr.SourceVisible}: visible}}
	scanner := newScanner(source)
	scanner.SkipObserve = true
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
	source := &fakeSource{text: map[[2]string][]string{{"w1:p1", herdr.SourceVisible}: visible}}
	scanner := newScanner(source)
	scanner.SkipObserve = true
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
	source := &fakeSource{text: map[[2]string][]string{{"w1:p1", herdr.SourceVisible}: visible}}
	scanner := newScanner(source)
	scanner.SkipObserve = true
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
	source := &fakeSource{text: map[[2]string][]string{{"w1:p1", herdr.SourceVisible}: visible}}
	scanner := newScanner(source)
	scanner.SkipObserve = true
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
	source := &fakeSource{text: paneText("w1:p1", "go https://a.io/x here")}
	scanner := newScanner(source)
	scanner.SkipObserve = true
	got := scanner.Links(context.Background(), panes("w1:p1"))
	if len(got) != 1 || got[0].URL != "https://a.io/x" {
		t.Fatalf("Links() = %+v", got)
	}
	if n := source.readCount("w1:p1", herdr.SourceUnwrapped); n != 0 {
		t.Fatalf("unwrapped reads = %d, want none without a wrapped URL", n)
	}
}

func TestNeedsUnwrapped(t *testing.T) {
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
			if got := needsUnwrapped(tc.visible); got != tc.want {
				t.Fatalf("needsUnwrapped(%q) = %v, want %v", tc.visible, got, tc.want)
			}
		})
	}
}
