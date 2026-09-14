package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/ui"
)

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

func TestItemsFor(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{URL: "https://a.io/x", Text: "https://a.io/x", Row: 2},
		{URL: "https://b.io/y", Text: "#232", Row: 5},
	}
	codes := []string{"a", "s"}

	got := itemsFor(found, codes)
	want := []ui.Item{
		{Code: "a", Text: "https://a.io/x", Row: 2},
		{Code: "s", Text: "#232", Row: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("itemsFor() = %+v, want %+v", got, want)
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

// A badge keeps its link's place in screen order, so a code always marks
// the same link.
func TestBadgesForKeepsScreenOrder(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{Row: 2, Col: 4, Before: 3, Text: "abcd"},
		{Row: 9, Col: 0, Text: "ab"},
		{Row: 3, Col: 7, Before: 1, Text: "abc"},
	}
	codes := []string{"a", "s", "d"}

	got := badgesFor(found, codes, []int{0, 1, 2})
	want := []overlay.Badge{
		{Row: 2, Col: 4, Before: 3, Width: 4, Code: "a"},
		{Row: 9, Col: 0, Width: 2, Code: "s"},
		{Row: 3, Col: 7, Before: 1, Width: 3, Code: "d"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("badgesFor() = %+v, want %+v", got, want)
	}
}

// Narrowing fades the hints it rules out instead of dropping them.
func TestBadgesForFadesTheRestWhenNarrowing(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{Row: 2, Col: 4, Text: "ab"},
		{Row: 9, Col: 0, Text: "ab"},
	}
	got := badgesFor(found, []string{"a", "s"}, []int{1})
	if len(got) != 2 {
		t.Fatalf("badgesFor() = %+v, want both links kept", got)
	}
	if !got[0].Dim {
		t.Fatal("the ruled-out hint should be dimmed")
	}
	if got[1].Dim {
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
