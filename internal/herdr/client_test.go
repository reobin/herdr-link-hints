package herdr

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

func TestParseScreenPanes(t *testing.T) {
	t.Parallel()
	layout := `{"result":{"layout":{"area":{"x":0,"y":0,"width":206,"height":79},"panes":[{"pane_id":"w1:p1","rect":{"x":0,"y":0,"width":206,"height":59}},{"pane_id":""},{"pane_id":"w1:p2","rect":{"x":0,"y":59,"width":206,"height":20}}]}}}`
	got, err := parseScreenPanes([]byte(layout), "w1:p1")
	if err != nil {
		t.Fatalf("parseScreenPanes: %v", err)
	}
	want := Layout{
		Area:  Rect{Width: 206, Height: 79},
		Panes: []Pane{{ID: "w1:p1", Width: 206, Height: 59}, {ID: "w1:p2", Y: 59, Width: 206, Height: 20}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseScreenPanes() = %+v, want %+v", got, want)
	}
}

func TestParseScreenPanesFallsBack(t *testing.T) {
	t.Parallel()
	for _, out := range []string{`{"result":{"layout":{"panes":[]}}}`, "not json"} {
		got, _ := parseScreenPanes([]byte(out), "w1:p9")
		if want := (Layout{Panes: []Pane{{ID: "w1:p9"}}}); !reflect.DeepEqual(got, want) {
			t.Fatalf("parseScreenPanes(%q) = %+v, want %+v", out, got, want)
		}
	}
	if _, err := parseScreenPanes([]byte("not json"), "w1:p9"); err == nil {
		t.Fatal("expected a parse error alongside the fallback")
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

	got, err := New(WithSocket(socket)).PaneLines(context.Background(), "w1:p1")
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

func TestScreenPanesOverSocket(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id": request["id"],
			"result": map[string]any{"type": "pane_layout", "layout": map[string]any{
				"area": map[string]any{"x": 0, "y": 0, "width": 206, "height": 59},
				"panes": []any{
					map[string]any{"pane_id": "w1:p1", "rect": map[string]any{"x": 0, "y": 0, "width": 206, "height": 59}},
					map[string]any{"pane_id": ""},
				},
			}},
		})}
	})

	got, err := New(WithSocket(socket)).ScreenPanes(context.Background(), "w1:p1")
	if err != nil {
		t.Fatalf("ScreenPanes: %v", err)
	}
	want := Layout{Area: Rect{Width: 206, Height: 59}, Panes: []Pane{{ID: "w1:p1", Width: 206, Height: 59}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScreenPanes() = %+v, want %+v", got, want)
	}
	request := <-requests
	if request["method"] != "pane.layout" {
		t.Fatalf("method = %v", request["method"])
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
	} else if want := (Layout{Panes: []Pane{{ID: "w1:p9"}}}); !reflect.DeepEqual(panes, want) {
		t.Fatalf("ScreenPanes() = %+v, want %+v", panes, want)
	}
	if _, err := client.PaneLines(context.Background(), "w1:p9"); err == nil {
		t.Fatal("expected an error when both paths fail")
	}
	if scroll, err := client.PaneScroll(context.Background(), "w1:p9"); err == nil {
		t.Fatal("expected an error when both paths fail")
	} else if scroll != (Scroll{}) {
		t.Fatalf("PaneScroll() = %+v, want a zero Scroll", scroll)
	}
}

// A socket that accepts and never answers burns the whole rpc timeout
// before the CLI is even tried, so the error it hides has to reach the log.
func TestFallbackLogsTheSocketError(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := New(WithSocket(socketPath(t)), WithLogger(log))

	if _, err := client.PaneLines(context.Background(), "w1:p9"); err == nil {
		t.Fatal("expected an error when both paths fail")
	}
	if got := logged.String(); !strings.Contains(got, "pane.read") {
		t.Fatalf("log = %q, want it to name the failed method", got)
	}
}

// One pane.list replaces a pane.get per pane, so the scroll baseline stays
// one round trip however many panes share the screen.
func TestPaneScrollsReadsEveryPaneInOneCall(t *testing.T) {
	t.Parallel()
	calls := make(chan map[string]any, 4)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		calls <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id": request["id"],
			"result": map[string]any{"panes": []any{
				map[string]any{"pane_id": "w1:p1", "scroll": map[string]any{"max_offset_from_bottom": 12, "viewport_rows": 57}},
				map[string]any{"pane_id": "w1:p2", "scroll": map[string]any{"max_offset_from_bottom": 0, "viewport_rows": 20}},
			}},
		})}
	})

	got, err := New(WithSocket(socket)).PaneScrolls(context.Background())
	if err != nil {
		t.Fatalf("PaneScrolls: %v", err)
	}
	want := map[string]Scroll{
		"w1:p1": {Offset: 12, ViewportRows: 57},
		"w1:p2": {Offset: 0, ViewportRows: 20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PaneScrolls() = %+v, want %+v", got, want)
	}
	if request := <-calls; request["method"] != "pane.list" {
		t.Fatalf("method = %v, want pane.list", request["method"])
	}
	if len(calls) != 0 {
		t.Fatalf("%d extra calls, want one for every pane", len(calls))
	}
}
