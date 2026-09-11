package herdr

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestGraphicsInfo(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id": request["id"],
			"result": map[string]any{
				"type":                "pane_graphics_info",
				"cell_width_px":       9,
				"cell_height_px":      20,
				"pane_visible":        true,
				"max_layers_per_pane": 16,
			},
		})}
	})

	info, err := New(WithSocket(socket)).GraphicsInfo(context.Background(), "w1:p1")
	if err != nil {
		t.Fatalf("GraphicsInfo: %v", err)
	}
	want := Graphics{CellWidthPx: 9, CellHeightPx: 20, PaneVisible: true, MaxLayers: 16}
	if info != want {
		t.Fatalf("GraphicsInfo() = %+v, want %+v", info, want)
	}

	request := <-requests
	if request["method"] != "pane.graphics.info" {
		t.Fatalf("method = %v", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["pane_id"] != "w1:p1" {
		t.Fatalf("params = %+v", params)
	}
}

func TestSetGraphics(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id":     request["id"],
			"result": map[string]any{"type": "pane_graphics_frame_ack", "sequence": 1, "revision": 1},
		})}
	})

	png := []byte{0x89, 'P', 'N', 'G'}
	err := New(WithSocket(socket)).SetGraphics(context.Background(), Frame{
		Pane:   "w1:p1",
		Layer:  "link-hints",
		ZIndex: 1000,
		PNG:    png,
		Width:  72,
		Height: 20,
		Row:    4,
		Col:    12,
		Rows:   1,
		Cols:   8,
	})
	if err != nil {
		t.Fatalf("SetGraphics: %v", err)
	}

	request := <-requests
	if request["method"] != "pane.graphics.set" {
		t.Fatalf("method = %v", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["pane_id"] != "w1:p1" || params["layer_id"] != "link-hints" || params["format"] != "png" {
		t.Fatalf("params = %+v", params)
	}
	if params["z_index"] != float64(1000) || params["image_width"] != float64(72) || params["image_height"] != float64(20) {
		t.Fatalf("params = %+v", params)
	}
	if params["data_base64"] != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("data_base64 = %v", params["data_base64"])
	}
	placement, _ := params["placement"].(map[string]any)
	if placement["viewport_row"] != float64(4) || placement["viewport_col"] != float64(12) {
		t.Fatalf("placement = %+v", placement)
	}
	if placement["grid_rows"] != float64(1) || placement["grid_cols"] != float64(8) {
		t.Fatalf("placement = %+v", placement)
	}
}

// Omitting layer_id would clear Herdr's "primary" layer instead of ours.
func TestClearGraphicsNamesTheLayer(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{"id": request["id"], "result": map[string]any{"type": "ok"}})}
	})

	if err := New(WithSocket(socket)).ClearGraphics(context.Background(), "w1:p1", "link-hints"); err != nil {
		t.Fatalf("ClearGraphics: %v", err)
	}

	request := <-requests
	if request["method"] != "pane.graphics.clear" {
		t.Fatalf("method = %v", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["pane_id"] != "w1:p1" || params["layer_id"] != "link-hints" {
		t.Fatalf("params = %+v", params)
	}
}

func TestOpenPane(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id": request["id"],
			"result": map[string]any{
				"type": "plugin_pane_opened",
				"plugin_pane": map[string]any{
					"plugin_id":  "herdr-link-hints",
					"entrypoint": "picker",
					"pane":       map[string]any{"pane_id": "w1:p7"},
				},
			},
		})}
	})

	pane, err := New(WithSocket(socket)).OpenPane(context.Background(), PaneOpen{
		Plugin:     "herdr-link-hints",
		Entrypoint: "picker",
		Placement:  "popup",
		Width:      "100%",
		Height:     "3",
		Focus:      true,
		Env:        map[string]string{"HINTS_MODE": "annotate"},
	})
	if err != nil {
		t.Fatalf("OpenPane: %v", err)
	}
	if pane != "w1:p7" {
		t.Fatalf("OpenPane() = %q, want w1:p7", pane)
	}

	request := <-requests
	if request["method"] != "plugin.pane.open" {
		t.Fatalf("method = %v", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["plugin_id"] != "herdr-link-hints" || params["entrypoint"] != "picker" || params["focus"] != true {
		t.Fatalf("params = %+v", params)
	}
	// A percentage stays a string and a cell count becomes a number:
	// Herdr accepts each only in its own shape.
	if params["width"] != "100%" || params["height"] != float64(3) {
		t.Fatalf("width = %#v, height = %#v", params["width"], params["height"])
	}
	env, _ := params["env"].(map[string]any)
	if env["HINTS_MODE"] != "annotate" {
		t.Fatalf("env = %+v", env)
	}
}

func TestPopupSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want any
		ok   bool
	}{
		{"80%", "80%", true},
		{"100%", "100%", true},
		{"3", 3, true},
		{"", nil, false},
		{"wide", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, ok := popupSize(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("popupSize(%q) = %#v, %v; want %#v, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}
