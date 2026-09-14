package herdr

import (
	"context"
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

func TestPaneLinesOverSocket(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id":     request["id"],
			"result": map[string]any{"type": "pane_read", "read": map[string]any{"text": "a\nb\n"}},
		})}
	})

	got, err := New(WithSocket(socket)).PaneLines(context.Background(), "w1:p1", SourceVisible, 0)
	if err != nil {
		t.Fatalf("PaneLines: %v", err)
	}
	if want := []string{"a", "b", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PaneLines() = %q, want %q", got, want)
	}

	request := <-requests
	if request["method"] != "pane.read" {
		t.Fatalf("method = %v", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["pane_id"] != "w1:p1" || params["source"] != "visible" || params["format"] != "text" {
		t.Fatalf("params = %+v", params)
	}
	if _, ok := params["lines"]; ok {
		t.Fatalf("params = %+v, want no lines when the extent is left to Herdr", params)
	}
}

func TestPaneLinesMapsSourceAndLines(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id":     request["id"],
			"result": map[string]any{"type": "pane_read", "read": map[string]any{"text": "a"}},
		})}
	})

	if _, err := New(WithSocket(socket)).PaneLines(context.Background(), "w1:p1", SourceUnwrapped, 10); err != nil {
		t.Fatalf("PaneLines: %v", err)
	}
	params, _ := (<-requests)["params"].(map[string]any)
	if params["source"] != "recent_unwrapped" {
		t.Fatalf("source = %v, want the socket enum spelling", params["source"])
	}
	if params["lines"] != float64(10) {
		t.Fatalf("lines = %v, want 10", params["lines"])
	}
}

func TestScreenPanesOverSocket(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id": request["id"],
			"result": map[string]any{"type": "pane_layout", "layout": map[string]any{
				"panes": []any{
					map[string]any{"pane_id": "w1:p1", "rect": map[string]any{"width": 206, "height": 59}},
					map[string]any{"pane_id": ""},
				},
			}},
		})}
	})

	got, err := New(WithSocket(socket)).ScreenPanes(context.Background(), "w1:p1")
	if err != nil {
		t.Fatalf("ScreenPanes: %v", err)
	}
	if want := []Pane{{ID: "w1:p1", Width: 206, Height: 59}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ScreenPanes() = %+v, want %+v", got, want)
	}
	request := <-requests
	if request["method"] != "pane.layout" {
		t.Fatalf("method = %v", request["method"])
	}
}

func TestPaneLabelsOverSocket(t *testing.T) {
	t.Parallel()
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		return [][]byte{mustJSON(t, map[string]any{
			"id": request["id"],
			"result": map[string]any{"type": "pane_list", "panes": []any{
				map[string]any{"pane_id": "w1:p1", "label": "neon"},
				map[string]any{"pane_id": "w1:p2", "label": ""},
			}},
		})}
	})

	got, err := New(WithSocket(socket)).PaneLabels(context.Background(), []string{"w1:p1", "w1:p2"})
	if err != nil {
		t.Fatalf("PaneLabels: %v", err)
	}
	if want := map[string]string{"w1:p1": "neon", "w1:p2": "p2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PaneLabels() = %+v, want %+v", got, want)
	}
}

func TestPaneScrollOverSocket(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id":     request["id"],
			"result": map[string]any{"type": "pane_info", "pane": map[string]any{"scroll": map[string]any{"max_offset_from_bottom": 17, "viewport_rows": 57}}},
		})}
	})

	got, err := New(WithSocket(socket)).PaneScroll(context.Background(), "w1:p1")
	if err != nil {
		t.Fatalf("PaneScroll: %v", err)
	}
	if want := (Scroll{Offset: 17, ViewportRows: 57}); got != want {
		t.Fatalf("PaneScroll() = %+v, want %+v", got, want)
	}
	request := <-requests
	if request["method"] != "pane.get" {
		t.Fatalf("method = %v", request["method"])
	}
}

// Not parallel: these cases point the client at sockets and binaries that
// do not exist, and read the environment.
func TestSocketFallsBackToCLI(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "no-such-herdr-bin")
	client := New(WithSocket(socketPath(t)))

	if panes, err := client.ScreenPanes(context.Background(), "w1:p9"); err == nil {
		t.Fatal("expected an error when both paths fail")
	} else if want := []Pane{{ID: "w1:p9"}}; !reflect.DeepEqual(panes, want) {
		t.Fatalf("ScreenPanes() = %+v, want %+v", panes, want)
	}
	if _, err := client.PaneLines(context.Background(), "w1:p9", SourceVisible, 0); err == nil {
		t.Fatal("expected an error when both paths fail")
	}
	if labels, err := client.PaneLabels(context.Background(), []string{"w1:p9"}); err == nil {
		t.Fatal("expected an error when both paths fail")
	} else if want := map[string]string{"w1:p9": "p9"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("PaneLabels() = %+v, want %+v", labels, want)
	}
	if scroll, err := client.PaneScroll(context.Background(), "w1:p9"); err == nil {
		t.Fatal("expected an error when both paths fail")
	} else if scroll != (Scroll{}) {
		t.Fatalf("PaneScroll() = %+v, want a zero Scroll", scroll)
	}
}
