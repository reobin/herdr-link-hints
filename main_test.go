package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
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

	got := itemsFor(found, labels, true)
	if got[0].Where != "neon" || got[1].Where != "w1:p2" {
		t.Fatalf("itemsFor() panes = %q, %q", got[0].Where, got[1].Where)
	}
	if codes := []string{got[0].Code, got[1].Code}; !reflect.DeepEqual(codes, []string{"a", "s"}) {
		t.Fatalf("itemsFor() codes = %+v", codes)
	}

	single := itemsFor(found, labels, false)
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

func TestProvisionalTitle(t *testing.T) {
	t.Parallel()
	if got := provisionalTitle([]string{"w1:p1", "w1:p2"}, "w1:p1"); got != "2 panes" {
		t.Fatalf("provisionalTitle() = %q", got)
	}
	if got := provisionalTitle([]string{"w1:p1"}, "w1:p1"); got != "pane w1:p1" {
		t.Fatalf("provisionalTitle() = %q", got)
	}
}
