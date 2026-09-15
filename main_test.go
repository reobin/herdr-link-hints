package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/theme"
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

func TestItemsFor(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{URL: "https://a.io/x", Text: "https://a.io/x", Row: 2, Pane: "w1:p1"},
		{URL: "https://b.io/y", Text: "#232", Row: 5, Pane: "w1:p2"},
	}

	codes := []string{"a", "s"}
	got := itemsFor(found, codes)
	if assigned := []string{got[0].Code, got[1].Code}; !reflect.DeepEqual(assigned, codes) {
		t.Fatalf("itemsFor() codes = %+v", assigned)
	}
}

// Without a graphics layer the pick continues as a code list: no badge
// redraws are wired, and nothing exits.
func TestNarrowOptsFallsBackToListWithoutALayer(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	found := []links.Link{{Pane: "w1:p1", Row: 1, Col: 2, Text: "ab"}}
	codes := []string{"a"}
	ctx := context.Background()

	if opts := narrowOpts(ctx, nil, found, codes); opts.OnNarrow != nil {
		t.Fatal("nil marker should leave OnNarrow unset so the pick continues as a list")
	}
	dead := newMarker(nil, log, theme.Colors{},
		[]herdr.Pane{{ID: "w1:p1", Width: 80, Height: 24}},
		map[string]herdr.Scroll{}, map[string]herdr.Graphics{})
	if opts := narrowOpts(ctx, dead, found, codes); opts.OnNarrow != nil {
		t.Fatal("dead marker should leave OnNarrow unset so the pick continues as a list")
	}
	live := newMarker(nil, log, theme.Colors{},
		[]herdr.Pane{{ID: "w1:p1", Width: 80, Height: 24}},
		map[string]herdr.Scroll{},
		map[string]herdr.Graphics{"w1:p1": {CellWidthPx: 9, CellHeightPx: 19, PaneVisible: true}})
	if !live.live() {
		t.Fatal("want a live marker for the control case")
	}
	if opts := narrowOpts(ctx, live, found, codes); opts.OnNarrow == nil {
		t.Fatal("live marker should redraw badges on narrow")
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

// The popup is a fixed content-sized box: wide enough for the spinner with
// its label and a three-digit count with its unit, tall enough to centre
// the count on the middle row. Herdr numbers are outer dimensions with a
// border cell on each side, so the asked shape is two bigger each way.
func TestPaneShape(t *testing.T) {
	t.Run("the fixed shape holds the readout", func(t *testing.T) {
		t.Parallel()
		width, height := paneShape()
		if width != "24" || height != "5" {
			t.Fatalf("paneShape() = %q, %q", width, height)
		}
	})
	t.Run("the fixed width holds the widest readout lines", func(t *testing.T) {
		t.Parallel()
		for _, text := range []string{"\u280b scanning", "999 links", "no match"} {
			if got := len([]rune(text)); got > contentCols {
				t.Fatalf("%q is %d cells, wider than contentCols %d", text, got, contentCols)
			}
		}
	})
	t.Run("the environment overrides both", func(t *testing.T) {
		t.Setenv("HINTS_WIDTH", "40")
		t.Setenv("HINTS_HEIGHT", "10")
		width, height := paneShape()
		if width != "40" || height != "10" {
			t.Fatalf("paneShape() = %q, %q", width, height)
		}
	})
}

// Not parallel: these cases set environment variables.
func TestPaneFor(t *testing.T) {
	t.Run("annotate gets the fixed popup", func(t *testing.T) {
		got := paneFor()
		if got.Placement != "popup" || got.Width != "24" || got.Height != "5" {
			t.Fatalf("paneFor() = %+v", got)
		}
	})
	// Only a popup takes a size, so any other placement must ask for none.
	t.Run("the environment overrides the placement", func(t *testing.T) {
		t.Setenv("HINTS_PLACEMENT", "overlay")
		got := paneFor()
		if got.Placement != "overlay" || got.Width != "" || got.Height != "" {
			t.Fatalf("paneFor() = %+v", got)
		}
	})
}
