package config

import "testing"

// Not parallel: every case sets environment variables.
func TestFocusedPanePrefersTheMostTrustedSource(t *testing.T) {
	t.Run("active pane wins", func(t *testing.T) {
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_id":"w1:p3"}`)
		t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
		t.Setenv("HERDR_PANE_ID", "w1:p2")
		pane, source := Load().FocusedPane()
		if pane != "w1:p1" || source != "HERDR_ACTIVE_PANE_ID" {
			t.Fatalf("FocusedPane() = %q from %q", pane, source)
		}
	})
	t.Run("falls back to the context blob", func(t *testing.T) {
		t.Setenv("HERDR_ACTIVE_PANE_ID", "")
		t.Setenv("HERDR_PANE_ID", "")
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_id":"w1:p9"}`)
		if pane, _ := Load().FocusedPane(); pane != "w1:p9" {
			t.Fatalf("FocusedPane() = %q", pane)
		}
	})
	t.Run("nothing to resolve", func(t *testing.T) {
		t.Setenv("HERDR_ACTIVE_PANE_ID", "")
		t.Setenv("HERDR_PANE_ID", "")
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "not json")
		pane, source := Load().FocusedPane()
		if pane != "" || source != "none" {
			t.Fatalf("FocusedPane() = %q from %q", pane, source)
		}
	})
	t.Run("every candidate is named for the log", func(t *testing.T) {
		if got := len(Load().PaneSources()); got != 3 {
			t.Fatalf("PaneSources() has %d entries, want 3", got)
		}
	})
}

// Not parallel: these cases set environment variables.
func TestPaneShape(t *testing.T) {
	t.Run("the fixed popup holds the readout", func(t *testing.T) {
		if PopupWidth != "14" || PopupHeight != "5" {
			t.Fatalf("popup = %sx%s", PopupWidth, PopupHeight)
		}
	})
	t.Run("the fixed width holds the widest readout lines", func(t *testing.T) {
		for _, text := range []string{"⠋ scanning", "999 links", "no match"} {
			if got := len([]rune(text)); got > contentCols {
				t.Fatalf("%q is %d cells, wider than contentCols %d", text, got, contentCols)
			}
		}
	})
}

func TestLoadReadsTheRest(t *testing.T) {
	t.Setenv("HINTS_DEBUG", "/tmp/hints.log")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "/state")
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("HINTS_HANDOFF", "/state/handoff-1.json")
	cfg := Load()
	if cfg.Debug != "/tmp/hints.log" || cfg.StateDir != "/state" ||
		cfg.TermProgram != "ghostty" || cfg.HandoffPath != "/state/handoff-1.json" {
		t.Fatalf("Load() = %+v", cfg)
	}
}
