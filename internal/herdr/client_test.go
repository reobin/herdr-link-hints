package herdr

import (
	"strings"
	"testing"
)

// Herdr answers with the whole screen, so a split tab must still come back
// as the one pane that was asked about.
func TestParsePaneRect(t *testing.T) {
	t.Parallel()
	layout := `{"result":{"layout":{"panes":[{"pane_id":"w1:p1","rect":{"x":0,"y":0,"width":206,"height":59}},{"pane_id":"w1:p2","rect":{"x":0,"y":59,"width":206,"height":20}}]}}}`
	got, err := parsePaneRect([]byte(layout), "w1:p2")
	if err != nil {
		t.Fatalf("parsePaneRect: %v", err)
	}
	if want := (Pane{ID: "w1:p2", Width: 206, Height: 20}); got != want {
		t.Fatalf("parsePaneRect() = %+v, want %+v", got, want)
	}
}

func TestParsePaneRectFallsBack(t *testing.T) {
	t.Parallel()
	for _, out := range []string{`{"result":{"layout":{"panes":[{"pane_id":"w1:p1"}]}}}`, "not json"} {
		got, err := parsePaneRect([]byte(out), "w1:p9")
		if err == nil {
			t.Fatalf("parsePaneRect(%q): expected an error", out)
		}
		if want := (Pane{ID: "w1:p9"}); got != want {
			t.Fatalf("parsePaneRect(%q) = %+v, want %+v", out, got, want)
		}
	}
}

func TestParsePaneInfo(t *testing.T) {
	t.Parallel()
	got, err := parsePaneInfo([]byte(`{"result":{"pane":{"label":"neon","scroll":{"max_offset_from_bottom":17,"viewport_rows":57}}}}`), "w1:p1")
	if err != nil {
		t.Fatalf("parsePaneInfo: %v", err)
	}
	if want := (PaneInfo{Label: "neon", Scroll: Scroll{Offset: 17, ViewportRows: 57}}); got != want {
		t.Fatalf("parsePaneInfo() = %+v, want %+v", got, want)
	}
}

// A pane Herdr has not named is still worth naming in the picker title.
func TestParsePaneInfoFallsBackToTheID(t *testing.T) {
	t.Parallel()
	got, err := parsePaneInfo([]byte(`{"result":{"pane":{}}}`), "w1:p1")
	if err != nil {
		t.Fatalf("parsePaneInfo: %v", err)
	}
	if want := (PaneInfo{Label: "p1"}); got != want {
		t.Fatalf("parsePaneInfo() = %+v, want %+v", got, want)
	}
	got, err = parsePaneInfo([]byte("nope"), "w1:p1")
	if err == nil || !strings.Contains(err.Error(), "parse pane get") {
		t.Fatalf("parsePaneInfo() error = %v, want a parse error", err)
	}
	if want := (PaneInfo{Label: "p1"}); got != want {
		t.Fatalf("parsePaneInfo() = %+v, want %+v", got, want)
	}
}

func TestShortID(t *testing.T) {
	t.Parallel()
	if got := shortID("w1:p1"); got != "p1" {
		t.Fatalf("shortID() = %q", got)
	}
	if got := shortID("bare"); got != "bare" {
		t.Fatalf("shortID() = %q", got)
	}
}
