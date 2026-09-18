// Package marks owns the graphics layers this plugin puts over a pane.
package marks

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

// Painter is the Herdr slice drawing needs.
type Painter interface {
	SetGraphics(ctx context.Context, f herdr.Frame) error
	ClearGraphics(ctx context.Context, pane, layer string) error
}

// Z order stacks the dim backdrop, then the frame, then the badges.
const (
	frameZ = 1000
	dimZ   = frameZ - 1
	badgeZ = frameZ + 1
)

// dimLayerID is the backdrop layer.
const dimLayerID = overlay.LayerID + "-dim"

func badgeLayerID(i int) string {
	return overlay.LayerID + "-" + strconv.Itoa(i)
}

// tileLayerID names a tile by the side it sits on; a whole frame keeps
// the bare id.
func tileLayerID(base, side string) string {
	if side == "" {
		return base
	}
	return base + "-" + side
}

// maxLayersTotal is Herdr's total graphics layer cap.
const maxLayersTotal = 64

// Marker owns per-pane graphics layers, kept past process exit.
// A pane holds frame tiles, or the dim backdrop tiles plus one layer per match.
type Marker struct {
	client    Painter
	log       *slog.Logger
	colors    theme.Colors
	popup     herdr.Rect
	views     map[string]paneView
	maxLayers map[string]int
	mu        sync.Mutex
	panes     map[string]*paneMarks
	closing   bool
	trail     *Trail
}

// Option overrides a New default.
type Option func(*Marker)

// WithTrail records what goes up, so a run that is killed outright can be
// cleaned up by the next one.
func WithTrail(t *Trail) Option {
	return func(m *Marker) { m.trail = t }
}

// WithPopup names the surface cells the picker popup will cover. Herdr
// hides an image that touches a popup, so nothing may reach into them.
func WithPopup(r herdr.Rect) Option {
	return func(m *Marker) { m.popup = r }
}

type paneView struct {
	cell  overlay.Cell
	size  overlay.Size
	avoid overlay.Rect
}

func (v paneView) scene(badges []overlay.Badge, colors theme.Colors) overlay.Scene {
	return overlay.Scene{Badges: badges, Colors: colors, Cell: v.cell, Viewport: v.size, Avoid: v.avoid}
}

// frameCost is the most tiles a frame on this pane takes.
func (v paneView) frameCost() int {
	if v.avoid.Empty() {
		return 1
	}
	return 4
}

// paneMarks is one pane's on-screen state.
type paneMarks struct {
	plan    overlay.Plan
	planned bool
	// frame and dim are the tile layers up.
	frame  []string
	shown  []overlay.Badge
	dim    []string
	layers map[int]overlay.Badge
}

// New keeps only drawable panes.
func New(client Painter, log *slog.Logger, colors theme.Colors, panes []herdr.Pane, scrolls map[string]herdr.Scroll, infos map[string]herdr.Graphics, opts ...Option) *Marker {
	m := &Marker{
		client:    client,
		log:       log,
		colors:    colors,
		views:     make(map[string]paneView, len(panes)),
		maxLayers: make(map[string]int, len(panes)),
		panes:     map[string]*paneMarks{},
	}
	for _, opt := range opts {
		opt(m)
	}
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
		size := Content(pane, scrolls[pane.ID].ViewportRows)
		avoid := Avoid(pane, size, m.popup)
		log.Debug("pane viewport", "pane", pane.ID, "rows", size.Rows, "cols", size.Cols,
			"cell_width_px", info.CellWidthPx, "cell_height_px", info.CellHeightPx,
			"max_layers", info.MaxLayers, "avoid", avoid)
		m.views[pane.ID] = paneView{
			cell:  overlay.Cell{Width: info.CellWidthPx, Height: info.CellHeightPx},
			size:  size,
			avoid: avoid,
		}
		m.maxLayers[pane.ID] = info.MaxLayers
	}
	return m
}

// Content strips the border off a pane's rect.
func Content(pane herdr.Pane, viewportRows int) overlay.Size {
	if viewportRows <= 0 || viewportRows > pane.Height {
		return overlay.Size{Rows: pane.Height, Cols: pane.Width}
	}
	return overlay.Size{Rows: viewportRows, Cols: pane.Width - (pane.Height - viewportRows)}
}

// Avoid is the popup's footprint in a pane's viewport cells, widened by a
// cell so a border miscount cannot leave a tile touching the popup.
func Avoid(pane herdr.Pane, size overlay.Size, popup herdr.Rect) overlay.Rect {
	if popup.Width <= 0 || popup.Height <= 0 {
		return overlay.Rect{}
	}
	border := (pane.Height - size.Rows) / 2
	footprint := overlay.Rect{
		Row:  popup.Y - (pane.Y + border) - 1,
		Col:  popup.X - (pane.X + border) - 1,
		Rows: popup.Height + 2,
		Cols: popup.Width + 2,
	}
	return footprint.Intersect(overlay.Rect{Rows: size.Rows, Cols: size.Cols})
}

func (m *Marker) marksFor(pane string) *paneMarks {
	marks, ok := m.panes[pane]
	if !ok {
		marks = &paneMarks{layers: map[int]overlay.Badge{}}
		m.panes[pane] = marks
	}
	return marks
}

// Adopt takes over the frame layers another process drew, per pane.
func (m *Marker) Adopt(drawn map[string][]string, badges map[string][]overlay.Badge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for pane, layers := range drawn {
		if _, ok := m.views[pane]; !ok || len(layers) == 0 {
			continue
		}
		marks := m.marksFor(pane)
		marks.frame = slices.Clone(layers)
		marks.shown = slices.Clone(badges[pane])
	}
}

// Drawn names the frame layers up on each pane.
func (m *Marker) Drawn() map[string][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	drawn := map[string][]string{}
	for pane, marks := range m.panes {
		if len(marks.frame) > 0 {
			drawn[pane] = slices.Clone(marks.frame)
		}
	}
	return drawn
}

func (m *Marker) Live() bool { return m != nil && len(m.views) > 0 }

// Prime puts the dim backdrop under the frame, off the open path.
func (m *Marker) Prime(ctx context.Context, badges map[string][]overlay.Badge) {
	var wg sync.WaitGroup
	for pane, view := range m.views {
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

func (m *Marker) primePane(ctx context.Context, pane string, view paneView, badges []overlay.Badge) {
	plan, err := overlay.NewPlan(view.scene(badges, m.colors))
	if err != nil {
		m.log.Debug("plan overlay failed", "pane", pane, "error", err)
		return
	}
	tiles, err := plan.Tiles(allDim(badges))
	if err != nil {
		m.log.Debug("render dim backdrop failed", "pane", pane, "error", err)
		return
	}
	// Skip if closed: a late backdrop would outlive the picker.
	if m.done() {
		return
	}
	up, err := m.setTiles(ctx, pane, dimLayerID, dimZ, tiles)
	if err != nil {
		m.log.Debug("set dim backdrop failed", "pane", pane, "error", err)
		m.clearLayers(ctx, pane, up)
		return
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		m.clearLayers(context.WithoutCancel(ctx), pane, up)
		return
	}
	defer m.mu.Unlock()
	marks := m.marksFor(pane)
	marks.plan, marks.planned = plan, true
	marks.dim = up
}

func (m *Marker) done() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closing
}

func allDim(badges []overlay.Badge) []overlay.Badge {
	out := make([]overlay.Badge, len(badges))
	for i, b := range badges {
		b.Dim, b.Typed = true, 0
		out[i] = b
	}
	return out
}

// claim is one pane's cost to narrow by layer: tiles held plus a layer
// per match.
type claim struct {
	pane    string
	matches int
	held    int
	perPane int
}

func (c claim) cost() int { return c.held + c.matches }

// layerBudget picks panes that narrow by layer, cheapest first.
func layerBudget(claims []claim, used, total int) map[string]bool {
	ordered := slices.Clone(claims)
	slices.SortStableFunc(ordered, func(a, b claim) int { return a.matches - b.matches })
	for _, c := range ordered {
		used += c.held
	}
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

// claims scores each pane for layered narrowing.
func (m *Marker) claims(badges map[string][]overlay.Badge) (claims []claim, used int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for pane, view := range m.views {
		marks := m.panes[pane]
		if len(badges[pane]) == 0 && (marks == nil || len(marks.frame) == 0) {
			continue
		}
		if marks == nil || len(marks.dim) == 0 || !marks.planned ||
			!marks.plan.SameLinks(badges[pane]) || !backdropShows(badges[pane]) {
			// This pane draws a frame instead.
			used += view.frameCost()
			if marks != nil {
				used += len(marks.dim)
			}
			continue
		}
		claims = append(claims, claim{
			pane:    pane,
			matches: matching(marks.plan, badges[pane]),
			held:    len(marks.dim) + max(len(marks.frame), view.frameCost()),
			perPane: m.maxLayers[pane],
		})
	}
	return claims, used
}

// backdropShows reports whether the dim backdrop still matches.
func backdropShows(badges []overlay.Badge) bool {
	for _, b := range badges {
		if b.Dim && b.Typed > 0 {
			return false
		}
	}
	return true
}

// matching counts badges still shown.
func matching(plan overlay.Plan, badges []overlay.Badge) int {
	n := 0
	for i := range plan.Placed() {
		if !badges[plan.Link(i)].Dim {
			n++
		}
	}
	return n
}

// Draw brings every pane up to date, skipping unchanged ones.
func (m *Marker) Draw(ctx context.Context, badges map[string][]overlay.Badge) {
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

// drawBadges narrows by layer, setting before clearing to avoid flicker.
func (m *Marker) drawBadges(ctx context.Context, pane string, view paneView, badges []overlay.Badge) {
	m.mu.Lock()
	marks := m.marksFor(pane)
	plan, up, frameUp := marks.plan, maps.Clone(marks.layers), len(marks.frame) > 0
	stale := !marks.planned || !plan.SameLinks(badges) || !backdropShows(badges)
	m.mu.Unlock()
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

// unchanged reports badges already on screen.
func (m *Marker) unchanged(pane string, badges []overlay.Badge) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	marks := m.panes[pane]
	return marks != nil && len(marks.frame) > 0 && slices.Equal(marks.shown, badges)
}

func (m *Marker) commitBadges(pane string, up map[int]overlay.Badge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.marksFor(pane).layers = up
}

// drawFrame re-encodes the viewport as tiles, replacing each in place and
// dropping the ones the new frame no longer needs.
func (m *Marker) drawFrame(ctx context.Context, pane string, view paneView, badges []overlay.Badge) {
	unchanged := m.unchanged(pane, badges)
	m.mu.Lock()
	marks := m.marksFor(pane)
	up := maps.Clone(marks.layers)
	was := slices.Clone(marks.frame)
	blank := len(badges) == 0 && len(was) == 0
	m.mu.Unlock()
	if blank || (unchanged && len(up) == 0) {
		return
	}

	if !unchanged {
		plan, err := overlay.NewPlan(view.scene(badges, m.colors))
		var tiles []overlay.Tile
		if err == nil {
			tiles, err = plan.Tiles(badges)
		}
		var set []string
		if err == nil {
			set, err = m.setTiles(ctx, pane, overlay.LayerID, frameZ, tiles)
		}
		if err != nil {
			m.log.Debug("frame failed", "pane", pane, "error", err)
			m.mu.Lock()
			marks.frame = union(was, set)
			m.mu.Unlock()
			m.clearPane(ctx, pane)
			return
		}
		stale := m.clearLayers(ctx, pane, difference(was, set))
		m.mu.Lock()
		marks.frame = union(set, stale)
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

// setTiles puts tiles up under base and reports the ids that made it.
func (m *Marker) setTiles(ctx context.Context, pane, base string, z int, tiles []overlay.Tile) ([]string, error) {
	var up []string
	for _, tile := range tiles {
		id := tileLayerID(base, tile.Side)
		if err := m.setLayer(ctx, pane, id, z, tile.Frame); err != nil {
			return up, err
		}
		up = append(up, id)
	}
	return up, nil
}

// clearLayers takes layers down and reports the ones that stayed up.
func (m *Marker) clearLayers(ctx context.Context, pane string, layers []string) []string {
	var stuck []string
	for _, layer := range layers {
		if err := m.clearLayer(ctx, pane, layer); err != nil {
			m.log.Debug("clear layer failed", "pane", pane, "layer", layer, "error", err)
			stuck = append(stuck, layer)
		}
	}
	return stuck
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func difference(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

func (m *Marker) setLayer(ctx context.Context, pane, layer string, z int, frame overlay.Frame) error {
	// Noted first: a kill between the two over-reports, which is harmless.
	m.trail.mark(pane, layer)
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

func (m *Marker) clearLayer(ctx context.Context, pane, layer string) error {
	return m.client.ClearGraphics(ctx, pane, layer)
}

// clearFrame takes the frame tiles down, keeping any that refused.
func (m *Marker) clearFrame(ctx context.Context, pane string) {
	m.mu.Lock()
	frame := slices.Clone(m.marksFor(pane).frame)
	m.mu.Unlock()
	stuck := m.clearLayers(ctx, pane, frame)
	m.mu.Lock()
	defer m.mu.Unlock()
	marks := m.marksFor(pane)
	marks.frame = stuck
	if len(stuck) == 0 {
		marks.shown = nil
	}
}

// Clear takes every layer down, panes in parallel so quitting stays quick.
func (m *Marker) Clear(ctx context.Context) {
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	var wg sync.WaitGroup
	for pane := range m.views {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.clearPane(ctx, pane)
		}()
	}
	wg.Wait()
	if m.stranded() == 0 {
		m.trail.done()
	}
}

// stranded counts panes whose layers would not come down.
func (m *Marker) stranded() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.panes)
}

// clearPane removes every layer this plugin owns.
func (m *Marker) clearPane(ctx context.Context, pane string) {
	m.mu.Lock()
	marks := m.panes[pane]
	if marks == nil {
		m.mu.Unlock()
		return
	}
	layers := make([]string, 0, len(marks.layers)+len(marks.frame)+len(marks.dim))
	for i := range marks.layers {
		layers = append(layers, badgeLayerID(i))
	}
	layers = append(layers, marks.frame...)
	layers = append(layers, marks.dim...)
	m.mu.Unlock()

	if stuck := m.clearLayers(ctx, pane, layers); len(stuck) > 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.panes, pane)
}
