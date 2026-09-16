package main

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/theme"
)

// dimLayerID names the backdrop: every badge, and the box around its
// link, in their ruled-out state. It sits under the bright frame and under
// the badge layers, and it never changes while the picker is open.
const dimLayerID = overlay.LayerID + "-dim"

// badgeLayerID names the layer one still-matching badge is drawn in.
func badgeLayerID(i int) string {
	return overlay.LayerID + "-" + strconv.Itoa(i)
}

// maxLayersTotal is Herdr's cap on graphics layers across every pane at
// once. Unlike the per-pane cap it is not reported by pane.graphics.info,
// so it comes from the server's own constant, PANE_GRAPHICS_MAX_LAYERS_TOTAL.
// A budget that has it wrong shows up as a failed set, which falls that
// pane back to a whole frame, so being wrong costs latency rather than
// leaving the overlay broken.
const maxLayersTotal = 64

// marker owns the graphics layers this plugin draws per pane. Herdr keeps
// a layer until it is cleared by name, past the exit of the process that
// set it.
//
// A pane is drawn one of two ways. Either it holds one full-viewport
// frame, which is what the action process puts up and what every pane fell
// back to before layers, or it holds the dim backdrop plus one small layer
// per badge that still matches. The second costs a few thousand pixels per
// keystroke instead of twelve million, which is the whole point, but
// Herdr's layer caps mean not every pane can have it at once.
type marker struct {
	client    *herdr.Client
	log       *slog.Logger
	colors    theme.Colors
	views     map[string]paneView
	maxLayers map[string]int
	mu        sync.Mutex
	panes     map[string]*paneMarks
	closing   bool
}

type paneView struct {
	cell overlay.Cell
	size overlay.Size
}

// paneMarks is what one pane currently has on screen. plan is the badge
// placement the layers were drawn from, held across keystrokes because it
// depends only on where the links are.
type paneMarks struct {
	plan    overlay.Plan
	planned bool
	frameUp bool
	shown   []overlay.Badge
	dimUp   bool
	layers  map[int]overlay.Badge
}

// newMarker keeps only the panes that can be drawn on. The graphics infos
// arrive pre-fetched: pick() gathers them alongside the link scan, so the
// marker build never serialises a round trip per pane.
func newMarker(client *herdr.Client, log *slog.Logger, colors theme.Colors, panes []herdr.Pane, scrolls map[string]herdr.Scroll, infos map[string]herdr.Graphics) *marker {
	views := make(map[string]paneView, len(panes))
	maxLayers := make(map[string]int, len(panes))
	for _, pane := range panes {
		info, ok := infos[pane.ID]
		if !ok {
			log.Debug("graphics info missing", "pane", pane.ID)
			continue
		}
		if !info.PaneVisible || info.CellWidthPx <= 0 || info.CellHeightPx <= 0 {
			log.Debug("pane cannot be drawn on", "pane", pane.ID, "visible", info.PaneVisible)
			continue
		}
		size := content(pane, scrolls[pane.ID].ViewportRows)
		log.Debug("pane viewport", "pane", pane.ID, "rows", size.Rows, "cols", size.Cols,
			"cell_width_px", info.CellWidthPx, "cell_height_px", info.CellHeightPx,
			"max_layers", info.MaxLayers)
		views[pane.ID] = paneView{
			cell: overlay.Cell{Width: info.CellWidthPx, Height: info.CellHeightPx},
			size: size,
		}
		maxLayers[pane.ID] = info.MaxLayers
	}
	return &marker{client: client, log: log, colors: colors, views: views,
		maxLayers: maxLayers, panes: map[string]*paneMarks{}}
}

// content strips the border off a pane's layout rect: the border is what
// the rect has that the viewport does not.
func content(pane herdr.Pane, viewportRows int) overlay.Size {
	if viewportRows <= 0 || viewportRows > pane.Height {
		return overlay.Size{Rows: pane.Height, Cols: pane.Width}
	}
	return overlay.Size{Rows: viewportRows, Cols: pane.Width - (pane.Height - viewportRows)}
}

// marksFor is the pane's record, created on first use.
func (m *marker) marksFor(pane string) *paneMarks {
	marks, ok := m.panes[pane]
	if !ok {
		marks = &paneMarks{layers: map[int]overlay.Badge{}}
		m.panes[pane] = marks
	}
	return marks
}

// adopt takes over a layer another process already put up, so the picker
// does not re-encode a frame that is on screen and does know to clear it.
func (m *marker) adopt(panes []string, badges map[string][]overlay.Badge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, pane := range panes {
		if _, ok := m.views[pane]; !ok {
			continue
		}
		marks := m.marksFor(pane)
		marks.frameUp = true
		marks.shown = slices.Clone(badges[pane])
	}
}

// drawnPanes names the panes with a layer up, for a process handing the
// picker over to another one.
func (m *marker) drawnPanes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	panes := make([]string, 0, len(m.panes))
	for pane, marks := range m.panes {
		if marks.frameUp {
			panes = append(panes, pane)
		}
	}
	slices.Sort(panes)
	return panes
}

// live reports whether there is anywhere to draw.
func (m *marker) live() bool { return m != nil && len(m.views) > 0 }

// prime puts the dim backdrop up under the bright frame that is already on
// screen: every badge and its link box drawn as though the badge had
// been ruled out. It is what lets a keystroke redraw a handful of small
// badge layers rather than re-encode the viewport.
//
// It is deliberately not on the open path. Encoding it costs about as much
// as the frame itself, and nothing is waiting on it - the hints are up, and
// the user has not typed yet. Both frames come off the same plan and so
// cover the same pixels, and the bright one is opaque, so putting this
// underneath changes nothing on screen.
func (m *marker) prime(ctx context.Context, badges map[string][]overlay.Badge) {
	var wg sync.WaitGroup
	for pane, view := range m.views {
		// A pane holding no links has nothing to dim, and the backdrop for
		// it would be a viewport-sized transparent image.
		if len(badges[pane]) == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.primePane(ctx, pane, view, badges[pane])
		}()
	}
	wg.Wait()
}

func (m *marker) primePane(ctx context.Context, pane string, view paneView, badges []overlay.Badge) {
	plan, err := overlay.NewPlan(overlay.Scene{
		Badges:   badges,
		Colors:   m.colors,
		Cell:     view.cell,
		Viewport: view.size,
	})
	if err != nil {
		m.log.Debug("plan overlay failed", "pane", pane, "error", err)
		return
	}
	frame, err := plan.Frame(allDim(badges))
	if err != nil {
		m.log.Debug("render dim backdrop failed", "pane", pane, "error", err)
		return
	}
	// The picker can finish while this is still in flight. A backdrop set
	// after clear() has run would stay on the pane past the process that
	// put it there, so the check happens on both sides of the call.
	if m.done() {
		return
	}
	if err := m.setLayer(ctx, pane, dimLayerID, dimZ, frame); err != nil {
		m.log.Debug("set dim backdrop failed", "pane", pane, "error", err)
		return
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		if err := m.clearLayer(context.WithoutCancel(ctx), pane, dimLayerID); err != nil {
			m.log.Debug("clear late dim backdrop failed", "pane", pane, "error", err)
		}
		return
	}
	defer m.mu.Unlock()
	marks := m.marksFor(pane)
	marks.plan, marks.planned = plan, true
	marks.dimUp = true
}

func (m *marker) done() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closing
}

// allDim is every badge as the backdrop shows it: ruled out, nothing typed.
func allDim(badges []overlay.Badge) []overlay.Badge {
	out := make([]overlay.Badge, len(badges))
	for i, b := range badges {
		b.Dim, b.Typed = true, 0
		out[i] = b
	}
	return out
}

// claim is what one pane would spend to narrow by badge layer: the
// backdrop, the bright frame it keeps up until the new badges are in
// place, and one layer per badge that still matches.
type claim struct {
	pane    string
	matches int
	perPane int
}

func (c claim) cost() int { return 2 + c.matches }

// layerBudget picks the panes that narrow by badge layer. Herdr caps
// layers twice - per pane, and across every pane at once - and a draw
// covers the whole screen, so eight panes each at the per-pane cap would be
// double the total. Budgeting per pane alone passes every test and fails on
// a busy screen. The cheapest panes are taken first, which fits the most of
// them; a pane left out re-renders its whole frame, one layer whatever it
// holds.
func layerBudget(claims []claim, used, total int) map[string]bool {
	ordered := slices.Clone(claims)
	slices.SortStableFunc(ordered, func(a, b claim) int { return a.matches - b.matches })
	// The backdrop and the frame are up on a claiming pane either way, so
	// only the badge layers are what going layered actually spends.
	used += 2 * len(ordered)
	layered := make(map[string]bool, len(ordered))
	for _, c := range ordered {
		if c.cost() > c.perPane || used+c.matches > total {
			continue
		}
		used += c.matches
		layered[c.pane] = true
	}
	return layered
}

// claims is what each pane would spend to narrow by layer, and the layers
// already held by the panes that cannot.
func (m *marker) claims(badges map[string][]overlay.Badge) (claims []claim, used int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for pane := range m.views {
		marks := m.panes[pane]
		// A pane with no links to mark and nothing up spends nothing: it
		// neither claims layers nor holds any to charge against the total.
		if len(badges[pane]) == 0 && (marks == nil || !marks.frameUp) {
			continue
		}
		// A plan that no longer describes these links would draw a code
		// against the wrong one. Re-scanning is the only way that happens,
		// and a whole frame is always correct.
		if marks == nil || !marks.dimUp || !marks.planned ||
			!marks.plan.SameLinks(badges[pane]) || !backdropShows(badges[pane]) {
			used++
			continue
		}
		claims = append(claims, claim{
			pane:    pane,
			matches: matching(marks.plan, badges[pane]),
			perPane: m.maxLayers[pane],
		})
	}
	return claims, used
}

// backdropShows reports whether the dim backdrop is still an accurate
// picture of every ruled-out badge on the pane. It is drawn once, with
// nothing typed, but hints.Badges gives a ruled-out badge the runes its
// code shares with what has been typed - "ad" keeps one after "as" - and
// that leading cell is drawn inverted. Those badges get no bright layer, so
// a keystroke that puts typed progress on one has to re-render the frame or
// the pane would quietly stop showing it.
//
// Codes are prefix-free, so this only arises once enough has been typed to
// share a rune without matching: rare, and the frame it costs is correct.
func backdropShows(badges []overlay.Badge) bool {
	for _, b := range badges {
		if b.Dim && b.Typed > 0 {
			return false
		}
	}
	return true
}

// matching counts the placed badges a typed prefix has not ruled out: one
// bright layer each.
func matching(plan overlay.Plan, badges []overlay.Badge) int {
	n := 0
	for i := range plan.Placed() {
		if !badges[plan.Link(i)].Dim {
			n++
		}
	}
	return n
}

// draw brings every pane up to date, in parallel, leaving the unchanged
// ones alone. A full-viewport render is 5-7ms at retina cell sizes and
// every keystroke that narrows redraws every pane, so re-encoding a pane
// whose badges did not move costs that much again for an identical image.
func (m *marker) draw(ctx context.Context, badges map[string][]overlay.Badge) {
	claims, used := m.claims(badges)
	layered := layerBudget(claims, used, maxLayersTotal)
	var wg sync.WaitGroup
	for pane, view := range m.views {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if layered[pane] {
				m.drawBadges(ctx, pane, view, badges[pane])
				return
			}
			m.drawFrame(ctx, pane, view, badges[pane])
		}()
	}
	wg.Wait()
}

// drawBadges narrows by layer: it puts the badges that still match up as
// their own small images over the dim backdrop, then takes down whatever
// is no longer wanted.
//
// The order matters. Setting before clearing means there is never a moment
// with the bright frame gone and the new badges not yet there, which would
// show as every hint on the pane dimming and coming back - on every
// keystroke, which is exactly the flicker this path exists to remove.
func (m *marker) drawBadges(ctx context.Context, pane string, view paneView, badges []overlay.Badge) {
	m.mu.Lock()
	marks := m.marksFor(pane)
	plan, up, frameUp := marks.plan, maps.Clone(marks.layers), marks.frameUp
	stale := !marks.planned || !plan.SameLinks(badges) || !backdropShows(badges)
	m.mu.Unlock()
	// The budget was decided against the plan as it stood then. prime can
	// still be committing one, so the pane is checked again here rather
	// than trusting a decision taken outside the lock.
	if stale {
		m.drawFrame(ctx, pane, view, badges)
		return
	}

	want := make(map[int]overlay.Badge, len(up))
	for i := range plan.Placed() {
		if b := badges[plan.Link(i)]; !b.Dim {
			want[i] = b
		}
	}
	if !frameUp && maps.Equal(up, want) {
		return
	}

	for i, b := range want {
		if was, ok := up[i]; ok && was == b {
			continue
		}
		frame, err := plan.Layer(i, badges)
		if err == nil {
			err = m.setLayer(ctx, pane, badgeLayerID(i), badgeZ, frame)
		}
		if err != nil {
			m.log.Debug("badge layer failed, falling back to a frame",
				"pane", pane, "badge", i, "error", err)
			m.commitBadges(pane, up)
			m.drawFrame(ctx, pane, view, badges)
			return
		}
		up[i] = b
	}
	for i := range up {
		if _, ok := want[i]; ok {
			continue
		}
		if err := m.clearLayer(ctx, pane, badgeLayerID(i)); err != nil {
			m.log.Debug("clear badge layer failed", "pane", pane, "badge", i, "error", err)
			continue
		}
		delete(up, i)
	}
	m.commitBadges(pane, up)
	if frameUp {
		m.clearFrame(ctx, pane)
	}
}

// unchanged reports whether a pane's frame already shows exactly these
// badges, so an identical image is never re-encoded and sent again.
func (m *marker) unchanged(pane string, badges []overlay.Badge) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	marks := m.panes[pane]
	return marks != nil && marks.frameUp && slices.Equal(marks.shown, badges)
}

func (m *marker) commitBadges(pane string, up map[int]overlay.Badge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.marksFor(pane).layers = up
}

// drawFrame re-encodes the whole viewport into the single layer the picker
// has always used, and takes down any badge layers the pane was narrowing
// with. Set before clear here too: the frame covers everything the badge
// layers drew, so taking them down afterwards is invisible.
func (m *marker) drawFrame(ctx context.Context, pane string, view paneView, badges []overlay.Badge) {
	unchanged := m.unchanged(pane, badges)
	m.mu.Lock()
	marks := m.marksFor(pane)
	up := maps.Clone(marks.layers)
	// Nothing to mark and no frame of ours up: rendering would encode a
	// viewport of transparent pixels, and setting it would cost a clear at
	// teardown. A stale frame still gets one, which is what takes it down.
	blank := len(badges) == 0 && !marks.frameUp
	m.mu.Unlock()
	if blank || (unchanged && len(up) == 0) {
		return
	}

	if !unchanged {
		frame, err := overlay.Render(overlay.Scene{
			Badges:   badges,
			Colors:   m.colors,
			Cell:     view.cell,
			Viewport: view.size,
		})
		if err == nil {
			err = m.setLayer(ctx, pane, overlay.LayerID, overlayZ, frame)
		}
		if err != nil {
			m.log.Debug("frame failed", "pane", pane, "error", err)
			m.clearPane(ctx, pane)
			return
		}
		m.mu.Lock()
		marks.frameUp = true
		marks.shown = slices.Clone(badges)
		m.mu.Unlock()
	}

	for i := range up {
		if err := m.clearLayer(ctx, pane, badgeLayerID(i)); err != nil {
			m.log.Debug("clear badge layer failed", "pane", pane, "badge", i, "error", err)
			continue
		}
		delete(up, i)
	}
	m.commitBadges(pane, up)
}

func (m *marker) setLayer(ctx context.Context, pane, layer string, z int, frame overlay.Frame) error {
	return m.client.SetGraphics(ctx, herdr.Frame{
		Pane:   pane,
		Layer:  layer,
		ZIndex: z,
		PNG:    frame.PNG,
		Width:  frame.Width,
		Height: frame.Height,
		Row:    frame.Row,
		Col:    frame.Col,
		Rows:   frame.Rows,
		Cols:   frame.Cols,
	})
}

func (m *marker) clearLayer(ctx context.Context, pane, layer string) error {
	return m.client.ClearGraphics(ctx, pane, layer)
}

func (m *marker) clearFrame(ctx context.Context, pane string) {
	if err := m.clearLayer(ctx, pane, overlay.LayerID); err != nil {
		m.log.Debug("clear frame failed", "pane", pane, "error", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	marks := m.marksFor(pane)
	marks.frameUp = false
	marks.shown = nil
}

func (m *marker) clear(ctx context.Context) {
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	for pane := range m.views {
		m.clearPane(ctx, pane)
	}
}

// clearPane takes every layer this plugin owns off a pane. It clears what
// it believes is up rather than everything it could have set, so a pane the
// picker never drew on costs no round trips.
func (m *marker) clearPane(ctx context.Context, pane string) {
	m.mu.Lock()
	marks := m.panes[pane]
	if marks == nil {
		m.mu.Unlock()
		return
	}
	layers := make([]string, 0, len(marks.layers)+2)
	for i := range marks.layers {
		layers = append(layers, badgeLayerID(i))
	}
	if marks.frameUp {
		layers = append(layers, overlay.LayerID)
	}
	if marks.dimUp {
		layers = append(layers, dimLayerID)
	}
	m.mu.Unlock()

	// Every layer is tried: one that will not come down is no reason to
	// leave the others on the user's screen.
	cleared := true
	for _, layer := range layers {
		if err := m.clearLayer(ctx, pane, layer); err != nil {
			m.log.Debug("clear overlay failed", "pane", pane, "layer", layer, "error", err)
			cleared = false
		}
	}
	if !cleared {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.panes, pane)
}
