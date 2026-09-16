package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/theme"
)

// graphicsCall is one pane.graphics.set or pane.graphics.clear the marker
// made, in the order the server saw it.
type graphicsCall struct {
	method string
	pane   string
	layer  string
}

func (c graphicsCall) String() string { return c.method + " " + c.pane + " " + c.layer }

// graphicsServer stands in for Herdr's socket. It records every graphics
// call and answers each one, or refuses the layers named in refuse, which
// is how a pane that has run out of layers is reproduced.
type graphicsServer struct {
	mu     sync.Mutex
	calls  []graphicsCall
	refuse map[string]bool
}

func (s *graphicsServer) record(c graphicsCall) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
	return s.refuse[c.layer]
}

func (s *graphicsServer) seen() []graphicsCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// layerSets is the layers a set landed on, in order.
func (s *graphicsServer) layerSets() []string {
	var out []string
	for _, c := range s.seen() {
		if c.method == "pane.graphics.set" {
			out = append(out, c.layer)
		}
	}
	return out
}

func indexOf(calls []graphicsCall, want string) int {
	for i, c := range calls {
		if c.String() == want {
			return i
		}
	}
	return -1
}

// startGraphicsServer listens on a unix socket the way the herdr package's
// own tests do, so the marker is exercised through the real client.
func startGraphicsServer(t *testing.T) (*graphicsServer, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hl-marker")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	server := &graphicsServer{refuse: map[string]bool{}}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				decoder := json.NewDecoder(conn)
				for {
					var request struct {
						ID     string `json:"id"`
						Method string `json:"method"`
						Params struct {
							Pane  string `json:"pane_id"`
							Layer string `json:"layer_id"`
						} `json:"params"`
					}
					if err := decoder.Decode(&request); err != nil {
						return
					}
					reply := map[string]any{"id": request.ID, "result": map[string]any{}}
					if server.record(graphicsCall{request.Method, request.Params.Pane, request.Params.Layer}) {
						reply = map[string]any{"id": request.ID, "error": map[string]any{
							"code": "limit_exceeded", "message": "too many layers",
						}}
					}
					line, err := json.Marshal(reply)
					if err != nil {
						return
					}
					if _, err := conn.Write(append(line, '\n')); err != nil {
						return
					}
				}
			}()
		}
	}()
	return server, socket
}

func testMarker(t *testing.T, socket string, panes ...string) *marker {
	t.Helper()
	views := make(map[string]paneView, len(panes))
	maxLayers := make(map[string]int, len(panes))
	for _, pane := range panes {
		views[pane] = paneView{
			cell: overlay.Cell{Width: 8, Height: 16},
			size: overlay.Size{Cols: 40, Rows: 8},
		}
		maxLayers[pane] = 16
	}
	return &marker{
		client:    herdr.New(herdr.WithSocket(socket)),
		log:       slog.New(slog.DiscardHandler),
		colors:    theme.Fallback(),
		views:     views,
		maxLayers: maxLayers,
		panes:     map[string]*paneMarks{},
	}
}

func testBadges(n int) []overlay.Badge {
	codes := []string{"as", "ad", "af", "ag", "ah", "aj", "ak", "al"}
	badges := make([]overlay.Badge, n)
	for i := range badges {
		badges[i] = overlay.Badge{
			Row: i, Col: 4, Before: 4, Width: 10, Code: codes[i%len(codes)],
		}
	}
	return badges
}

// narrow marks every badge but the ones in keep as ruled out.
func narrow(badges []overlay.Badge, keep ...int) []overlay.Badge {
	out := slices.Clone(badges)
	for i := range out {
		out[i].Dim = !slices.Contains(keep, i)
		if !out[i].Dim {
			out[i].Typed = 1
		}
	}
	return out
}

// TestDrawBadgesSetsBeforeClearing pins the order the swap happens in. The
// bright frame has to stay up until the new badge layers are in place:
// clearing it first leaves a window, short but real, in which the pane
// shows nothing but the dim backdrop. Herdr repaints on its own schedule,
// so that window is a full-pane flash on every keystroke - which is the
// flicker this whole path exists to remove.
func TestDrawBadgesSetsBeforeClearing(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1")
	ctx := context.Background()

	badges := testBadges(4)
	all := map[string][]overlay.Badge{"w1:p1": badges}
	m.adopt([]string{"w1:p1"}, all)
	m.prime(ctx, all)

	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(badges, 1)})

	calls := server.seen()
	cleared := indexOf(calls, "pane.graphics.clear w1:p1 "+overlay.LayerID)
	if cleared < 0 {
		t.Fatalf("the bright frame was never taken down: %v", calls)
	}
	set := -1
	for i, c := range calls {
		if c.method == "pane.graphics.set" && strings.HasPrefix(c.layer, overlay.LayerID+"-") &&
			c.layer != dimLayerID {
			set = i
		}
	}
	if set < 0 {
		t.Fatalf("no badge layer was set: %v", calls)
	}
	if set > cleared {
		t.Fatalf("the frame was cleared at %d before the badge layer was set at %d: %v",
			cleared, set, calls)
	}
}

// TestDrawBadgesRedrawsOnlyWhatChanged is the win itself: narrowing again
// must touch the badges whose state moved and leave the rest alone.
func TestDrawBadgesRedrawsOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1")
	ctx := context.Background()

	badges := testBadges(4)
	all := map[string][]overlay.Badge{"w1:p1": badges}
	m.adopt([]string{"w1:p1"}, all)
	m.prime(ctx, all)
	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(badges, 0, 1, 2)})

	before := len(server.seen())
	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(badges, 0, 1, 2)})
	if after := len(server.seen()); after != before {
		t.Fatalf("an unchanged narrowing made %d more calls", after-before)
	}

	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(badges, 0)})
	var sets, clears int
	for _, c := range server.seen()[before:] {
		switch c.method {
		case "pane.graphics.set":
			sets++
		case "pane.graphics.clear":
			clears++
		}
	}
	// Badge 0 kept its state, so only the two that stopped matching move.
	if sets != 0 || clears != 2 {
		t.Fatalf("dropping two matches made %d sets and %d clears, want 0 and 2", sets, clears)
	}
}

// TestDrawFallsBackToAFrameWhenALayerIsRefused covers the path that would
// otherwise rot: Herdr turning a set down mid-narrow. The pane has to end
// up showing a correct whole frame, with no half-applied badge layers left
// behind.
func TestDrawFallsBackToAFrameWhenALayerIsRefused(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1")
	ctx := context.Background()

	badges := testBadges(4)
	all := map[string][]overlay.Badge{"w1:p1": badges}
	m.adopt([]string{"w1:p1"}, all)
	m.prime(ctx, all)

	server.mu.Lock()
	for i := range 4 {
		server.refuse[badgeLayerID(i)] = true
	}
	server.mu.Unlock()

	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(badges, 1, 2)})

	if !slices.Contains(server.layerSets(), overlay.LayerID) {
		t.Fatalf("a refused badge layer should fall the pane back to a frame: %v", server.seen())
	}
	m.mu.Lock()
	marks := m.panes["w1:p1"]
	up, frameUp := len(marks.layers), marks.frameUp
	m.mu.Unlock()
	if !frameUp {
		t.Fatal("the pane should be back on its frame")
	}
	if up != 0 {
		t.Fatalf("%d badge layers were left behind after falling back", up)
	}
}

// TestClearTakesDownEveryLayer: a layer Herdr accepted outlives the process
// that set it, so anything left behind stays on the user's screen.
func TestClearTakesDownEveryLayer(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1")
	ctx := context.Background()

	badges := testBadges(3)
	all := map[string][]overlay.Badge{"w1:p1": badges}
	m.adopt([]string{"w1:p1"}, all)
	m.prime(ctx, all)
	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(badges, 0)})
	m.clear(ctx)

	set := map[string]bool{}
	for _, c := range server.seen() {
		switch c.method {
		case "pane.graphics.set":
			set[c.layer] = true
		case "pane.graphics.clear":
			delete(set, c.layer)
		}
	}
	if len(set) != 0 {
		t.Fatalf("layers left on the pane after clear: %v", set)
	}
}

func claimsOf(matches ...int) []claim {
	claims := make([]claim, len(matches))
	for i, n := range matches {
		claims[i] = claim{pane: string(rune('a' + i)), matches: n, perPane: 16}
	}
	return claims
}

func spend(claims []claim, layered map[string]bool, used int) int {
	total := used + 2*len(claims)
	for _, c := range claims {
		if layered[c.pane] {
			total += c.matches
		}
	}
	return total
}

// TestLayerBudgetHoldsTheGlobalCap is the case per-pane budgeting passes
// and a busy screen fails: eight panes each inside the 16-layer per-pane
// cap would claim well past the 64 Herdr allows across all of them.
func TestLayerBudgetHoldsTheGlobalCap(t *testing.T) {
	t.Parallel()
	claims := claimsOf(13, 13, 13, 13, 13, 13, 13, 13)
	layered := layerBudget(claims, 0, maxLayersTotal)
	if total := spend(claims, layered, 0); total > maxLayersTotal {
		t.Fatalf("budget spent %d layers, over the %d cap", total, maxLayersTotal)
	}
	if len(layered) == 0 {
		t.Fatal("no pane got layers at all, so nothing narrows cheaply")
	}
	if len(layered) == len(claims) {
		t.Fatal("every pane got layers, which cannot fit the total cap")
	}
}

// TestLayerBudgetTakesTheCheapestPanesFirst: fitting the most panes is what
// keeps the most of the screen on the cheap path.
func TestLayerBudgetTakesTheCheapestPanesFirst(t *testing.T) {
	t.Parallel()
	claims := claimsOf(14, 1, 14, 1, 14, 1)
	layered := layerBudget(claims, 0, 20)
	for _, c := range claims {
		if c.matches == 1 && !layered[c.pane] {
			t.Fatalf("a pane needing one layer was left out: %v", layered)
		}
	}
	if total := spend(claims, layered, 0); total > 20 {
		t.Fatalf("budget spent %d layers, over the 20 allowed", total)
	}
}

// TestLayerBudgetRefusesPastThePerPaneCap: one pane cannot narrow by layer
// however much global room there is.
func TestLayerBudgetRefusesPastThePerPaneCap(t *testing.T) {
	t.Parallel()
	claims := []claim{{pane: "a", matches: 15, perPane: 16}}
	if layered := layerBudget(claims, 0, maxLayersTotal); layered["a"] {
		t.Fatal("15 badges plus the backdrop and the frame is over a 16-layer pane")
	}
	claims = []claim{{pane: "a", matches: 14, perPane: 16}}
	if layered := layerBudget(claims, 0, maxLayersTotal); !layered["a"] {
		t.Fatal("14 badges plus the backdrop and the frame fits a 16-layer pane")
	}
}

// TestLayerBudgetCountsPanesItCannotHelp: a pane still holding a frame
// takes a layer from the same global pool.
func TestLayerBudgetCountsPanesItCannotHelp(t *testing.T) {
	t.Parallel()
	claims := claimsOf(10)
	if layered := layerBudget(claims, maxLayersTotal-11, maxLayersTotal); layered["a"] {
		t.Fatal("the layers already held by other panes were not counted")
	}
	if layered := layerBudget(claims, 0, maxLayersTotal); !layered["a"] {
		t.Fatal("with the pool free the pane should narrow by layer")
	}
}

// TestDrawUsesAFrameUntilTheBackdropIsUp: prime runs off the critical path,
// so a keystroke can land before it finishes. Until it does, the pane has
// no backdrop for badge layers to sit on and must re-render its frame.
func TestDrawUsesAFrameUntilTheBackdropIsUp(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1")
	ctx := context.Background()

	badges := testBadges(3)
	m.adopt([]string{"w1:p1"}, map[string][]overlay.Badge{"w1:p1": badges})
	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(badges, 0)})

	for _, layer := range server.layerSets() {
		if layer != overlay.LayerID {
			t.Fatalf("drew layer %q before the backdrop was up: %v", layer, server.seen())
		}
	}
}

// TestDrawRejectsAPlanFromADifferentScan: a plan placed the codes against
// one set of links. Reusing it after a rescan would draw a code the user
// has already seen against a different link, and they would open a URL
// they were not looking at. A whole frame is always correct.
func TestDrawRejectsAPlanFromADifferentScan(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1")
	ctx := context.Background()

	badges := testBadges(3)
	all := map[string][]overlay.Badge{"w1:p1": badges}
	m.adopt([]string{"w1:p1"}, all)
	m.prime(ctx, all)

	moved := slices.Clone(badges)
	moved[1].Col += 3
	before := len(server.seen())
	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": narrow(moved, 0)})

	for _, c := range server.seen()[before:] {
		if c.method == "pane.graphics.set" && c.layer != overlay.LayerID {
			t.Fatalf("reused a stale plan to set %q: %v", c.layer, server.seen()[before:])
		}
	}
}

func TestBadgeLayerIDsAreDistinctFromTheOthers(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{overlay.LayerID: true, dimLayerID: true}
	for i := range 32 {
		id := badgeLayerID(i)
		if seen[id] {
			t.Fatalf("badge layer %d reuses the id %q", i, id)
		}
		seen[id] = true
	}
}

// TestPrimeStrandsNothingAfterClear is the failure mode that outlives the
// process: prime runs off the critical path, so the user can pick a link
// while it is still in flight. A backdrop that lands after clear() has run
// would stay on the pane with nothing left to take it down.
func TestPrimeStrandsNothingAfterClear(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1")
	ctx := context.Background()

	badges := testBadges(3)
	all := map[string][]overlay.Badge{"w1:p1": badges}
	m.adopt([]string{"w1:p1"}, all)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.prime(ctx, all)
	}()
	m.clear(ctx)
	wg.Wait()
	// clear() can win the race outright, in which case prime never set
	// anything. Either way nothing may be left up.
	m.clear(ctx)

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

// TestDrawFallsBackWhenARuledOutBadgeShowsTypedProgress: the backdrop is
// drawn once with nothing typed, but hints.Badges gives a ruled-out badge
// the runes its code shares with what was typed - "ad" keeps one after
// "as" - and draws that cell inverted. No bright layer covers a ruled-out
// badge, so the pane has to re-render its frame or it would silently stop
// showing the prefix.
func TestDrawFallsBackWhenARuledOutBadgeShowsTypedProgress(t *testing.T) {
	t.Parallel()
	server, socket := startGraphicsServer(t)
	m := testMarker(t, socket, "w1:p1")
	ctx := context.Background()

	badges := testBadges(4)
	all := map[string][]overlay.Badge{"w1:p1": badges}
	m.adopt([]string{"w1:p1"}, all)
	m.prime(ctx, all)

	shared := narrow(badges, 0)
	shared[1].Typed = 1
	before := len(server.seen())
	m.draw(ctx, map[string][]overlay.Badge{"w1:p1": shared})

	for _, c := range server.seen()[before:] {
		if c.method == "pane.graphics.set" && c.layer != overlay.LayerID {
			t.Fatalf("narrowed by layer with typed progress on a ruled-out badge: %v",
				server.seen()[before:])
		}
	}
	if !slices.Contains(server.layerSets(), overlay.LayerID) {
		t.Fatalf("the pane should have re-rendered its frame: %v", server.seen()[before:])
	}
}

// TestBackdropShows is the condition itself, stated plainly.
func TestBackdropShows(t *testing.T) {
	t.Parallel()
	badges := testBadges(3)
	if !backdropShows(narrow(badges, 0)) {
		t.Fatal("a plain narrowing leaves the backdrop accurate")
	}
	typedOnDim := narrow(badges, 0)
	typedOnDim[2].Typed = 1
	if backdropShows(typedOnDim) {
		t.Fatal("typed progress on a ruled-out badge is not in the backdrop")
	}
}
