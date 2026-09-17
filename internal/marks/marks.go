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

// maxLayersTotal is Herdr's total graphics layer cap.
const maxLayersTotal = 64

// Marker owns per-pane graphics layers, kept past process exit.
// A pane holds one full frame or the dim backdrop plus one layer per match.
type Marker struct {
	client    Painter
	log       *slog.Logger
	colors    theme.Colors
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

type paneView struct {
	cell overlay.Cell
	size overlay.Size
}

// paneMarks is one pane's on-screen state.
type paneMarks struct {
	plan    overlay.Plan
	planned bool
	frameUp bool
	shown   []overlay.Badge
	dimUp   bool
	layers  map[int]overlay.Badge
}

// New keeps only drawable panes.
func New(client Painter, log *slog.Logger, colors theme.Colors, panes []herdr.Pane, scrolls map[string]herdr.Scroll, infos map[string]herdr.Graphics, opts ...Option) *Marker {
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
		size := Content(pane, scrolls[pane.ID].ViewportRows)
		log.Debug("pane viewport", "pane", pane.ID, "rows", size.Rows, "cols", size.Cols,
			"cell_width_px", info.CellWidthPx, "cell_height_px", info.CellHeightPx,
			"max_layers", info.MaxLayers)
		views[pane.ID] = paneView{
			cell: overlay.Cell{Width: info.CellWidthPx, Height: info.CellHeightPx},
			size: size,
		}
		maxLayers[pane.ID] = info.MaxLayers
	}
	m := &Marker{client: client, log: log, colors: colors, views: views,
		maxLayers: maxLayers, panes: map[string]*paneMarks{}}
	for _, opt := range opts {
		opt(m)
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

func (m *Marker) marksFor(pane string) *paneMarks {
	marks, ok := m.panes[pane]
	if !ok {
		marks = &paneMarks{layers: map[int]overlay.Badge{}}
		m.panes[pane] = marks
	}
	return marks
}

// Adopt takes over layers another process drew.
func (m *Marker) Adopt(panes []string, badges map[string][]overlay.Badge) {
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

// DrawnPanes names panes with a layer up.
func (m *Marker) DrawnPanes() []string {
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
	// Skip if closed: a late backdrop would outlive the picker.
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

// claim is one pane's cost to narrow by layer.
type claim struct {
	pane    string
	matches int
	perPane int
}

func (c claim) cost() int { return 2 + c.matches }

// layerBudget picks panes that narrow by layer, cheapest first.
func layerBudget(claims []claim, used, total int) map[string]bool {
	ordered := slices.Clone(claims)
	slices.SortStableFunc(ordered, func(a, b claim) int { return a.matches - b.matches })
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

// claims scores each pane for layered narrowing.
func (m *Marker) claims(badges map[string][]overlay.Badge) (claims []claim, used int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for pane := range m.views {
		marks := m.panes[pane]
		if len(badges[pane]) == 0 && (marks == nil || !marks.frameUp) {
			continue
		}
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
	plan, up, frameUp := marks.plan, maps.Clone(marks.layers), marks.frameUp
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
	return marks != nil && marks.frameUp && slices.Equal(marks.shown, badges)
}

func (m *Marker) commitBadges(pane string, up map[int]overlay.Badge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.marksFor(pane).layers = up
}

// drawFrame re-encodes the whole viewport.
func (m *Marker) drawFrame(ctx context.Context, pane string, view paneView, badges []overlay.Badge) {
	unchanged := m.unchanged(pane, badges)
	m.mu.Lock()
	marks := m.marksFor(pane)
	up := maps.Clone(marks.layers)
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
			err = m.setLayer(ctx, pane, overlay.LayerID, frameZ, frame)
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

func (m *Marker) clearFrame(ctx context.Context, pane string) {
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
