package marks

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/overlay"
)

// Calibrated against two bordered 103x59 panes on a 206x59 surface, with
// the default 14x5 popup centred astride both.
func TestAvoidMapsThePopupIntoEachPane(t *testing.T) {
	t.Parallel()
	popup := herdr.Rect{X: 96, Y: 27, Width: 14, Height: 5}
	left := herdr.Pane{ID: "w1:p1", X: 0, Y: 0, Width: 103, Height: 59}
	right := herdr.Pane{ID: "w1:p2", X: 103, Y: 0, Width: 103, Height: 59}
	size := Content(left, 57)

	// Shifted by the pane border, and a cell wider on every side.
	if got, want := Avoid(left, size, popup), (overlay.Rect{Row: 25, Col: 94, Rows: 7, Cols: 7}); got != want {
		t.Fatalf("Avoid(left) = %+v, want %+v", got, want)
	}
	if got, want := Avoid(right, size, popup), (overlay.Rect{Row: 25, Col: 0, Rows: 7, Cols: 7}); got != want {
		t.Fatalf("Avoid(right) = %+v, want %+v", got, want)
	}

	far := herdr.Pane{ID: "w1:p3", X: 0, Y: 59, Width: 206, Height: 20}
	if got := Avoid(far, Content(far, 18), popup); !got.Empty() {
		t.Fatalf("Avoid(far) = %+v, want nothing to avoid on a pane the popup misses", got)
	}
	if got := Avoid(left, size, herdr.Rect{}); !got.Empty() {
		t.Fatalf("Avoid() = %+v with no popup, want nothing", got)
	}
}

func avoidingMarker(t *testing.T, socket string, avoid overlay.Rect) *Marker {
	t.Helper()
	m := testMarker(t, socket, "w1:p1")
	view := m.views["w1:p1"]
	view.avoid = avoid
	m.views["w1:p1"] = view
	return m
}

// No tile may reach into the footprint, so the frame comes up as named
// side tiles.
func TestDrawTilesAroundThePopup(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	avoid := overlay.Rect{Row: 2, Col: 14, Rows: 3, Cols: 12}
	m := avoidingMarker(t, socket, avoid)
	ctx := context.Background()

	badges := []overlay.Badge{
		{Row: 0, Col: 6, Before: 6, Width: 8, Code: "as"},
		{Row: 7, Col: 20, Before: 4, Width: 6, Code: "ad"},
		{Row: 3, Col: 2, Before: 2, Width: 6, Code: "af"},
		{Row: 3, Col: 30, Before: 5, Width: 6, Code: "ag"},
	}
	m.Draw(ctx, map[string][]overlay.Badge{"w1:p1": badges})

	sets := server.layerSets()
	if slices.Contains(sets, overlay.LayerID) {
		t.Fatalf("drew the whole frame over the popup: %v", sets)
	}
	for _, side := range []string{"top", "bottom", "left", "right"} {
		if !slices.Contains(sets, overlay.LayerID+"-"+side) {
			t.Fatalf("no %s tile among %v", side, sets)
		}
	}
	for _, c := range server.seen() {
		if c.method == "pane.graphics.set" && !c.at.Intersect(avoid).Empty() {
			t.Fatalf("layer %q at %+v reaches into the popup footprint %+v", c.layer, c.at, avoid)
		}
	}
	if drawn := m.Drawn()["w1:p1"]; len(drawn) != 4 {
		t.Fatalf("Drawn() = %v, want the four tiles", drawn)
	}

	// A scan with only the top link left leaves only the top tile up.
	m.Draw(ctx, map[string][]overlay.Badge{"w1:p1": badges[:1]})
	if drawn := m.Drawn()["w1:p1"]; !slices.Equal(drawn, []string{overlay.LayerID + "-top"}) {
		t.Fatalf("Drawn() = %v with only the top link left", drawn)
	}
	m.Clear(ctx)
	up := map[string]bool{}
	for _, c := range server.seen() {
		switch c.method {
		case "pane.graphics.set":
			up[c.layer] = true
		case "pane.graphics.clear":
			delete(up, c.layer)
		}
	}
	if len(up) != 0 {
		t.Fatalf("layers left on the pane after clear: %v", up)
	}
}

// The backdrop tiles the same way, and badge layers keep off the popup too.
func TestPrimeAndLayersKeepClearOfThePopup(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	avoid := overlay.Rect{Row: 2, Col: 14, Rows: 3, Cols: 12}
	m := avoidingMarker(t, socket, avoid)
	ctx := context.Background()

	badges := []overlay.Badge{
		{Row: 0, Col: 6, Before: 6, Width: 8, Code: "as"},
		{Row: 3, Col: 8, Before: 8, Width: 20, Code: "ad"},
	}
	all := map[string][]overlay.Badge{"w1:p1": badges}
	m.Prime(ctx, all)
	m.Draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(badges, 1)})

	var dims, layers int
	for _, c := range server.seen() {
		if c.method != "pane.graphics.set" {
			continue
		}
		if !c.at.Intersect(avoid).Empty() {
			t.Fatalf("layer %q at %+v reaches into the popup footprint %+v", c.layer, c.at, avoid)
		}
		switch {
		case strings.HasPrefix(c.layer, dimLayerID):
			dims++
		case c.layer == badgeLayerID(1):
			layers++
		}
	}
	if dims == 0 || layers == 0 {
		t.Fatalf("want backdrop tiles and a badge layer, got %v", server.layerSets())
	}
}

// A pane whose frame tiles around the popup budgets for four tiles.
func TestClaimsCountTheTilesAFrameTakes(t *testing.T) {
	t.Parallel()
	_, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1", "w1:p2")
	view := m.views["w1:p2"]
	view.avoid = overlay.Rect{Row: 2, Col: 14, Rows: 3, Cols: 12}
	m.views["w1:p2"] = view

	badges := map[string][]overlay.Badge{"w1:p1": testBadges(2), "w1:p2": testBadges(2)}
	if _, used := m.claims(badges); used != 5 {
		t.Fatalf("claims() used = %d, want one whole frame plus four tiles", used)
	}
}

func TestTileLayerIDsAreDistinctFromTheOthers(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, base := range []string{overlay.LayerID, dimLayerID} {
		for _, side := range []string{"", "top", "bottom", "left", "right"} {
			id := tileLayerID(base, side)
			if seen[id] {
				t.Fatalf("tile %s/%q reuses the id %q", base, side, id)
			}
			seen[id] = true
		}
	}
	for i := range 32 {
		if seen[badgeLayerID(i)] {
			t.Fatalf("badge layer %d collides with a tile id", i)
		}
	}
}
