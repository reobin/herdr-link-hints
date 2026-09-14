package main

import (
	"bytes"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/overlay"
)

// Every debug line carries how long the process has been running, so the
// startup cost is measured rather than estimated.
func TestLoggerStampsDuration(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(&durationHandler{
		Handler: slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		start:   time.Now().Add(-time.Second),
	})
	log.Debug("picker pane", "rows", 5)
	out := buf.String()
	if !strings.Contains(out, "duration_ms=") {
		t.Fatalf("log line has no duration: %q", out)
	}
}

func TestTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		result   herdr.Activation
		err      error
		fallback string
		wantURL  string
		wantDone bool
	}{
		{
			name:     "resolved but not handled falls back to the browser",
			result:   herdr.Activation{URL: "https://a.io/x"},
			fallback: "https://a.io/x",
			wantURL:  "https://a.io/x",
		},
		{
			name:     "handled means Herdr opened it",
			result:   herdr.Activation{URL: "https://a.io/x", Handled: true},
			fallback: "https://a.io/x",
			wantURL:  "https://a.io/x",
			wantDone: true,
		},
		{
			name:     "a resolved url beats the one we read off screen",
			result:   herdr.Activation{URL: "https://r.io/real"},
			fallback: "https://t.io/typed",
			wantURL:  "https://r.io/real",
		},
		{
			name:     "a failed activation keeps the fallback",
			err:      errors.New("no socket"),
			fallback: "https://t.io/typed",
			wantURL:  "https://t.io/typed",
		},
		{
			name:     "a handled result is ignored when the call errored",
			result:   herdr.Activation{URL: "https://a.io/x", Handled: true},
			err:      errors.New("no socket"),
			fallback: "https://t.io/typed",
			wantURL:  "https://t.io/typed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			url, done := target(tc.result, tc.err, tc.fallback)
			if url != tc.wantURL || done != tc.wantDone {
				t.Fatalf("target() = %q, %v; want %q, %v", url, done, tc.wantURL, tc.wantDone)
			}
		})
	}
}

func TestScreenTitle(t *testing.T) {
	t.Parallel()
	labels := map[string]string{"w1:p1": "neon"}
	if got := screenTitle([]string{"w1:p1"}, labels, "w1:p1"); got != "pane neon" {
		t.Errorf("screenTitle() = %q", got)
	}
	if got := screenTitle([]string{"w1:p1"}, nil, "w1:p1"); got != "pane w1:p1" {
		t.Errorf("screenTitle() = %q", got)
	}
	if got := screenTitle([]string{"w1:p1", "w1:p2"}, labels, "w1:p1"); got != "2 panes" {
		t.Errorf("screenTitle() = %q", got)
	}
}

func TestItemsFor(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{URL: "https://a.io/x", Text: "https://a.io/x", Row: 2, Pane: "w1:p1"},
		{URL: "https://b.io/y", Text: "#232", Row: 5, Pane: "w1:p2"},
	}
	labels := map[string]string{"w1:p1": "neon"}

	codes := []string{"a", "s"}
	got := itemsFor(found, codes, labels, true)
	if got[0].Where != "neon" || got[1].Where != "w1:p2" {
		t.Fatalf("itemsFor() panes = %q, %q", got[0].Where, got[1].Where)
	}
	if assigned := []string{got[0].Code, got[1].Code}; !reflect.DeepEqual(assigned, codes) {
		t.Fatalf("itemsFor() codes = %+v", assigned)
	}

	single := itemsFor(found, codes, labels, false)
	if single[0].Where != "" || single[1].Where != "" {
		t.Fatal("a single-pane screen should not label rows with a pane")
	}
}

// Not parallel: these cases set environment variables.
func TestFocusedPane(t *testing.T) {
	t.Run("active pane wins", func(t *testing.T) {
		t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
		t.Setenv("HERDR_PANE_ID", "w1:p2")
		if got := focusedPane(); got != "w1:p1" {
			t.Fatalf("focusedPane() = %q", got)
		}
	})
	t.Run("falls back to the context blob", func(t *testing.T) {
		t.Setenv("HERDR_ACTIVE_PANE_ID", "")
		t.Setenv("HERDR_PANE_ID", "")
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_id":"w1:p9"}`)
		if got := focusedPane(); got != "w1:p9" {
			t.Fatalf("focusedPane() = %q", got)
		}
	})
	t.Run("nothing to resolve", func(t *testing.T) {
		t.Setenv("HERDR_ACTIVE_PANE_ID", "")
		t.Setenv("HERDR_PANE_ID", "")
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "not json")
		if got := focusedPane(); got != "" {
			t.Fatalf("focusedPane() = %q", got)
		}
	})
}

func TestGrownBy(t *testing.T) {
	t.Parallel()
	if got := clampGrowth(12, 5); got != 7 {
		t.Fatalf("clampGrowth() = %d", got)
	}
	if got := clampGrowth(3, 9); got != 0 {
		t.Fatalf("clampGrowth() = %d, want no negative shift", got)
	}
}

func TestPaneIDs(t *testing.T) {
	t.Parallel()
	got := paneIDs([]herdr.Pane{{ID: "w1:p1", Width: 206, Height: 59}, {ID: "w1:p2"}})
	if want := []string{"w1:p1", "w1:p2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paneIDs() = %+v, want %+v", got, want)
	}
}

func TestBadgesForGroupsByPane(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{Row: 2, Col: 4, Before: 3, Text: "abcd", Pane: "w1:p1"},
		{Row: 9, Col: 0, Text: "ab", Pane: "w1:p2"},
		{Row: 3, Col: 7, Before: 1, Text: "abc", Pane: "w1:p1"},
	}
	codes := []string{"a", "s", "d"}

	all := badgesFor(found, codes, []int{0, 1, 2})
	want := map[string][]overlay.Badge{
		"w1:p1": {
			{Row: 2, Col: 4, Before: 3, Width: 4, Code: "a"},
			{Row: 3, Col: 7, Before: 1, Width: 3, Code: "d"},
		},
		"w1:p2": {{Row: 9, Col: 0, Width: 2, Code: "s"}},
	}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("badgesFor() = %+v, want %+v", all, want)
	}
}

// Narrowing fades the hints it rules out instead of dropping them.
func TestBadgesForFadesTheRestWhenNarrowing(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{Row: 2, Col: 4, Text: "ab", Pane: "w1:p1"},
		{Row: 9, Col: 0, Text: "ab", Pane: "w1:p1"},
	}
	got := badgesFor(found, []string{"a", "s"}, []int{1})
	if len(got["w1:p1"]) != 2 {
		t.Fatalf("badgesFor() = %+v, want both links kept", got)
	}
	if !got["w1:p1"][0].Dim {
		t.Fatal("the ruled-out hint should be dimmed")
	}
	if got["w1:p1"][1].Dim {
		t.Fatal("the matching hint should stay bright")
	}
}

func TestContentStripsThePaneBorder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		pane         herdr.Pane
		viewportRows int
		want         overlay.Size
	}{
		// The live numbers this was calibrated against.
		{"bordered", herdr.Pane{Width: 206, Height: 59}, 57, overlay.Size{Cols: 204, Rows: 57}},
		{"borderless", herdr.Pane{Width: 80, Height: 24}, 24, overlay.Size{Cols: 80, Rows: 24}},
		{"unknown viewport", herdr.Pane{Width: 80, Height: 24}, 0, overlay.Size{Cols: 80, Rows: 24}},
		{"viewport larger than the rect", herdr.Pane{Width: 80, Height: 24}, 99, overlay.Size{Cols: 80, Rows: 24}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := content(tc.pane, tc.viewportRows); got != tc.want {
				t.Fatalf("content(%+v, %d) = %+v, want %+v", tc.pane, tc.viewportRows, got, tc.want)
			}
		})
	}
}

// retinaCell is the shape this was measured against: cells twice as tall
// as they are wide.
var retinaCell = herdr.Graphics{CellWidthPx: 19, CellHeightPx: 42}

// Not parallel: these cases set environment variables.
func TestPaneShape(t *testing.T) {
	t.Run("annotate is square in pixels, not in cells", func(t *testing.T) {
		width, height := paneShape(modeAnnotate, retinaCell)
		if width != "11" || height != "5" {
			t.Fatalf("paneShape(%q) = %q, %q", modeAnnotate, width, height)
		}
	})
	t.Run("a terminal that reported no cell size still gets a shape", func(t *testing.T) {
		width, height := paneShape(modeAnnotate, herdr.Graphics{})
		if width != "10" || height != "5" {
			t.Fatalf("paneShape(%q) = %q, %q", modeAnnotate, width, height)
		}
	})
	t.Run("the list fallback keeps room for a list", func(t *testing.T) {
		width, height := paneShape(modeList, retinaCell)
		if width != "80%" || height != "60%" {
			t.Fatalf("paneShape(%q) = %q, %q", modeList, width, height)
		}
	})
	t.Run("the environment overrides both", func(t *testing.T) {
		t.Setenv("HINTS_WIDTH", "40")
		t.Setenv("HINTS_HEIGHT", "10")
		width, height := paneShape(modeAnnotate, retinaCell)
		if width != "40" || height != "10" {
			t.Fatalf("paneShape(%q) = %q, %q", modeAnnotate, width, height)
		}
	})
}

// A box that is square in cells is twice as tall as it is wide on screen.
func TestSquareColsTracksTheCellAspect(t *testing.T) {
	t.Parallel()
	for _, cell := range []herdr.Graphics{retinaCell, {CellWidthPx: 9, CellHeightPx: 19}} {
		cols := squareCols(cell)
		wide := cols * cell.CellWidthPx
		tall := annotateRows * cell.CellHeightPx
		if off := max(wide, tall) - min(wide, tall); off > cell.CellWidthPx {
			t.Fatalf("cell %+v: %dx%d px is not square within a cell", cell, wide, tall)
		}
	}
}

// Not parallel: these cases set environment variables.
func TestPaneFor(t *testing.T) {
	t.Run("annotate gets the square popup", func(t *testing.T) {
		got := paneFor(modeAnnotate, retinaCell)
		if got.Placement != "popup" || got.Width != "11" || got.Height != "5" {
			t.Fatalf("paneFor(%q) = %+v", modeAnnotate, got)
		}
	})
	t.Run("the list fallback stays a sized popup", func(t *testing.T) {
		got := paneFor(modeList, retinaCell)
		if got.Placement != "popup" || got.Width != "80%" || got.Height != "60%" {
			t.Fatalf("paneFor(%q) = %+v", modeList, got)
		}
	})
	// Only a popup takes a size, so any other placement must ask for none.
	t.Run("the environment overrides the placement", func(t *testing.T) {
		t.Setenv("HINTS_PLACEMENT", "overlay")
		got := paneFor(modeAnnotate, retinaCell)
		if got.Placement != "overlay" || got.Width != "" || got.Height != "" {
			t.Fatalf("paneFor() = %+v", got)
		}
	})
}
