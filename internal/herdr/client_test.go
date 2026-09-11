package herdr

import (
	"reflect"
	"testing"
)

func TestParseScreenPanes(t *testing.T) {
	t.Parallel()
	layout := `{"result":{"layout":{"panes":[{"pane_id":"w1:p1","rect":{"x":0,"y":0,"width":206,"height":59}},{"pane_id":""},{"pane_id":"w1:p2","rect":{"x":0,"y":59,"width":206,"height":20}}]}}}`
	got, err := parseScreenPanes([]byte(layout), "w1:p1")
	if err != nil {
		t.Fatalf("parseScreenPanes: %v", err)
	}
	want := []Pane{{ID: "w1:p1", Width: 206, Height: 59}, {ID: "w1:p2", Width: 206, Height: 20}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseScreenPanes() = %+v, want %+v", got, want)
	}
}

func TestParseScreenPanesFallsBack(t *testing.T) {
	t.Parallel()
	for _, out := range []string{`{"result":{"layout":{"panes":[]}}}`, "not json"} {
		got, _ := parseScreenPanes([]byte(out), "w1:p9")
		if want := []Pane{{ID: "w1:p9"}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("parseScreenPanes(%q) = %+v, want %+v", out, got, want)
		}
	}
	if _, err := parseScreenPanes([]byte("not json"), "w1:p9"); err == nil {
		t.Fatal("expected a parse error alongside the fallback")
	}
}

func TestParsePaneLabels(t *testing.T) {
	t.Parallel()
	list := `{"result":{"panes":[{"pane_id":"w1:p1","label":"neon"},{"pane_id":"w1:p2","label":""},{"pane_id":"w1:p3","label":"other"}]}}`
	got, err := parsePaneLabels([]byte(list), defaultLabels([]string{"w1:p1", "w1:p2"}))
	if err != nil {
		t.Fatalf("parsePaneLabels: %v", err)
	}
	want := map[string]string{"w1:p1": "neon", "w1:p2": "p2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePaneLabels() = %+v, want %+v", got, want)
	}
}

func TestParsePaneLabelsKeepsDefaultsOnBadJSON(t *testing.T) {
	t.Parallel()
	got, err := parsePaneLabels([]byte("nope"), defaultLabels([]string{"w1:p1"}))
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if want := map[string]string{"w1:p1": "p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePaneLabels() = %+v, want %+v", got, want)
	}
}

func TestParsePaneScroll(t *testing.T) {
	t.Parallel()
	got, err := parsePaneScroll([]byte(`{"result":{"pane":{"scroll":{"max_offset_from_bottom":17,"viewport_rows":57}}}}`))
	if err != nil {
		t.Fatalf("parsePaneScroll: %v", err)
	}
	if want := (Scroll{Offset: 17, ViewportRows: 57}); got != want {
		t.Fatalf("parsePaneScroll() = %+v, want %+v", got, want)
	}
	if got, _ := parsePaneScroll([]byte(`{"result":{"pane":{}}}`)); got != (Scroll{}) {
		t.Fatalf("parsePaneScroll() = %+v, want a zero Scroll when scroll is absent", got)
	}
	if _, err := parsePaneScroll([]byte("nope")); err == nil {
		t.Fatal("expected a parse error")
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
