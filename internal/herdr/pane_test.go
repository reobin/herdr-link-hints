package herdr

import (
	"context"
	"testing"
)

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
