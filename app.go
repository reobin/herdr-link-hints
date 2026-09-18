package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/reobin/herdr-link-hints/internal/browse"
	"github.com/reobin/herdr-link-hints/internal/config"
	"github.com/reobin/herdr-link-hints/internal/handoff"
	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/hints"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/marks"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/scan"
	"github.com/reobin/herdr-link-hints/internal/theme"
	"github.com/reobin/herdr-link-hints/internal/ui"
)

// client is the Herdr slice the two flows use. *herdr.Client satisfies it;
// tests supply a fake.
type client interface {
	scan.Source
	marks.Painter
	ScreenPanes(ctx context.Context, pane string) (herdr.Layout, error)
	PaneScrolls(ctx context.Context) (map[string]herdr.Scroll, error)
	PaneScroll(ctx context.Context, pane string) (herdr.Scroll, error)
	GraphicsInfos(ctx context.Context, panes []string) map[string]herdr.Graphics
	ActivateLink(ctx context.Context, pane string, row, col int) (herdr.Activation, error)
	OpenPane(ctx context.Context, p herdr.PaneOpen) (string, error)
}

// themer is the colour source, so a test can answer without a tty.
type themer func() theme.Colors

// app carries what both flows need, injected rather than read from the environment.
type app struct {
	cfg    config.Config
	log    *slog.Logger
	client client
	trail  *marks.Trail
}

// open scans and draws before asking for the picker pane.
func (a *app) open(ctx context.Context) int {
	// Before the draw, never beside it: reclaiming clears the same layer
	// IDs on the same panes that prepare is about to set.
	marks.Reclaim(ctx, a.client, a.log, a.cfg.StateDir)
	spec := a.paneSpec()
	marker, drew := a.prepare(ctx, spec.Env)
	pane, err := a.client.OpenPane(ctx, spec)
	if err != nil {
		a.log.Debug("open picker pane failed", "placement", spec.Placement, "error", err)
		// No picker is coming to clear it.
		if drew {
			marker.Clear(context.WithoutCancel(ctx))
		}
		return exitFailed
	}
	attrs := []any{"placement", spec.Placement, "width", spec.Width, "height", spec.Height}
	if pane != "" {
		attrs = append(attrs, "pane", pane)
	}
	a.log.Debug("picker pane opened", attrs...)
	return exitOK
}

// prepare scans, draws, and leaves the result for the picker pane.
// Failures fall back to scanning in the picker.
func (a *app) prepare(ctx context.Context, env map[string]string) (*marks.Marker, bool) {
	focused := a.focusedPane()
	if focused == "" {
		return nil, false
	}
	p := a.gather(ctx, focused)

	// No tty here, so read the theme cache directly.
	colors, cached := theme.Load(a.cfg.TermProgram)
	p.Colors, p.HasColors = colors, cached

	var marker *marks.Marker
	if cached {
		marker = a.marker(colors, p)
		if marker.Live() {
			marker.Draw(ctx, firstBadges(p.Found, hints.Codes(len(p.Found), hints.DefaultAlphabet)))
			p.Drawn = marker.Drawn()
		}
	} else {
		a.log.Debug("theme cache miss, leaving the drawing to the picker pane")
	}

	path, err := handoff.Write(a.cfg.StateDir, p)
	if err != nil {
		a.log.Debug("handoff not written, the picker will scan for itself", "error", err)
		return marker, marker != nil
	}
	env[handoff.EnvVar] = path
	return marker, marker != nil
}

// paneSpec picks the placement. The picker always floats as a popup,
// which never reflows the pane.
func (a *app) paneSpec() herdr.PaneOpen {
	spec := herdr.PaneOpen{
		Plugin:     pluginID,
		Entrypoint: entrypoint,
		Placement:  "popup",
		Focus:      true,
		Env:        map[string]string{},
	}
	// Carry debug across to the pane process.
	if a.cfg.Debug != "" {
		spec.Env["HINTS_DEBUG"] = a.cfg.Debug
	}
	spec.Width, spec.Height = config.PopupWidth, config.PopupHeight
	return spec
}

func (a *app) pick(ctx context.Context, term *ui.Terminal, colors themer) int {
	rows, cols := term.Size()
	a.log.Debug("picker pane", "rows", rows, "cols", cols)

	// Prefer the handoff, else scan here.
	p, handed := handoff.Read(a.cfg.HandoffPath, a.log)
	if !handed {
		focused := a.focusedPane()
		if focused == "" {
			term.Pause("no pane")
			return exitCancelled
		}
		scanning := term.Spin("scanning")
		p = a.gather(ctx, focused)
		scanning()
	}
	a.log.Debug("picker input", "pane", p.Focused, "links", len(p.Found), "from_action", handed)

	if !p.HasColors {
		p.Colors = colors()
	}
	found := p.Found

	codes := hints.Codes(len(found), hints.DefaultAlphabet)
	marker := a.marker(p.Colors, p)
	defer marker.Clear(context.WithoutCancel(ctx))
	// Keep the action frame; don't re-encode it.
	marker.Adopt(p.Drawn, firstBadges(found, codes))
	// Prime the backdrop while the user reads.
	if marker.Live() && len(found) > 0 {
		go marker.Prime(ctx, firstBadges(found, codes))
	}
	if len(found) == 0 {
		marker.Clear(ctx)
		term.Pause("no links")
		return exitCancelled
	}

	index, picked := ui.Pick(term, itemsFor(found, codes), narrowOpts(ctx, marker, found, codes))
	if !picked {
		return exitCancelled
	}
	choice := found[index]

	scanner := &scan.Scanner{Source: a.client, Log: a.log}
	row, col, located := scanner.Locate(ctx, choice, a.grownBy(ctx, choice.Pane, p.Scrolls))
	if !located {
		term.Pause("scrolled")
		return exitFailed
	}
	// Clear before opening.
	marker.Clear(ctx)

	url, handled := a.activate(ctx, choice, row, col)
	if handled {
		term.Printf("\nOpened %s via Herdr.\n", url)
		return exitOK
	}
	if err := browse.Open(url); err != nil {
		a.log.Debug("browser open failed", "url", url, "error", err)
		term.Pause("failed")
		return exitFailed
	}
	term.Printf("\nOpened %s in browser.\n", url)
	return exitOK
}

// marker draws on the scanned panes, keeping clear of where the popup goes.
func (a *app) marker(colors theme.Colors, p handoff.Payload) *marks.Marker {
	return marks.New(a.client, a.log, colors, p.Panes, p.Scrolls, p.Infos,
		marks.WithTrail(a.trail), marks.WithPopup(p.Popup))
}

// popupRect is where the picker popup will sit, zero when the surface
// cannot hold one.
func popupRect(area herdr.Rect) herdr.Rect {
	rect, ok := herdr.PopupRect(area, config.PopupWidth, config.PopupHeight)
	if !ok {
		return herdr.Rect{}
	}
	return rect
}

// focusedPane takes the first source that named a pane.
func (a *app) focusedPane() string {
	for _, source := range a.cfg.PaneSources() {
		a.log.Debug("focused pane candidate", "source", source.Name, "value", source.Value)
	}
	pane, source := a.cfg.FocusedPane()
	a.log.Debug("focused pane", "pane", pane, "source", source)
	return pane
}

// gather reads layout, scroll baseline, and links.
func (a *app) gather(ctx context.Context, focused string) handoff.Payload {
	// pane.list carries scroll, pane.layout none, so they run together.
	// Both precede the snapshot they baseline.
	var (
		layout  herdr.Layout
		listed  map[string]herdr.Scroll
		layoutW sync.WaitGroup
	)
	layoutW.Add(2)
	go func() {
		defer layoutW.Done()
		var err error
		if layout, err = a.client.ScreenPanes(ctx, focused); err != nil {
			a.log.Debug("pane layout failed", "pane", focused, "error", err)
		}
	}()
	go func() {
		defer layoutW.Done()
		var err error
		if listed, err = a.client.PaneScrolls(ctx); err != nil {
			a.log.Debug("pane list failed", "error", err)
		}
	}()
	layoutW.Wait()

	panes := layout.Panes
	ids := paneIDs(panes)
	scrolls := a.scrollsFor(ctx, ids, listed)

	scanner := &scan.Scanner{Source: a.client, Log: a.log}
	scanInput := scanPanes(panes, scrolls)
	// Cursor at the viewport bottom, where the newest output is.
	cursors := make(map[string]int, len(scanInput))
	for _, pane := range scanInput {
		cursors[pane.ID] = pane.Rows - 1
	}
	// Graphics infos ride alongside the scan.
	var (
		infos   map[string]herdr.Graphics
		scanned []links.Link
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		infos = a.client.GraphicsInfos(ctx, ids)
	}()
	go func() {
		defer wg.Done()
		scanned = scanner.Links(ctx, scanInput)
	}()
	wg.Wait()

	return handoff.Payload{
		Focused: focused,
		Panes:   panes,
		Scrolls: scrolls,
		Infos:   infos,
		Found:   hints.Rank(scanned, focused, cursors),
		Popup:   popupRect(layout.Area),
	}
}

// scrollsFor fills scroll gaps with pane.get.
func (a *app) scrollsFor(ctx context.Context, ids []string, listed map[string]herdr.Scroll) map[string]herdr.Scroll {
	scrolls := make(map[string]herdr.Scroll, len(ids))
	var missing []string
	for _, id := range ids {
		if scroll, ok := listed[id]; ok {
			scrolls[id] = scroll
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return scrolls
	}
	a.log.Debug("pane list missed panes, reading them one by one", "panes", missing)
	for id, scroll := range a.paneScrolls(ctx, missing) {
		scrolls[id] = scroll
	}
	return scrolls
}

func (a *app) paneScrolls(ctx context.Context, panes []string) map[string]herdr.Scroll {
	scrolls := make(map[string]herdr.Scroll, len(panes))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, pane := range panes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scroll, err := a.client.PaneScroll(ctx, pane)
			if err != nil {
				a.log.Debug("pane scroll failed", "pane", pane, "error", err)
			}
			mu.Lock()
			scrolls[pane] = scroll
			mu.Unlock()
		}()
	}
	wg.Wait()
	return scrolls
}

// grownBy reports scroll growth since the scan.
func (a *app) grownBy(ctx context.Context, pane string, before map[string]herdr.Scroll) int {
	now, err := a.client.PaneScroll(ctx, pane)
	if err != nil {
		a.log.Debug("pane scroll failed", "pane", pane, "error", err)
		return 0
	}
	return clampGrowth(now.Offset, before[pane].Offset)
}

func (a *app) activate(ctx context.Context, choice links.Link, row, col int) (string, bool) {
	result, err := a.client.ActivateLink(ctx, choice.Pane, row, col)
	if err != nil {
		a.log.Debug("activate failed", "pane", choice.Pane, "row", row, "col", col, "error", err)
	}
	return target(result, err, choice.URL)
}

// clampGrowth ignores scrolling back.
func clampGrowth(now, before int) int {
	if grown := now - before; grown > 0 {
		return grown
	}
	return 0
}

// target prefers Herdr's URL, normalizing it.
func target(result herdr.Activation, err error, fallback string) (string, bool) {
	if err != nil {
		return fallback, false
	}
	url := fallback
	if result.URL != "" {
		url = links.Normalize(result.URL)
	}
	return url, result.Handled
}

func paneIDs(panes []herdr.Pane) []string {
	ids := make([]string, len(panes))
	for i, pane := range panes {
		ids[i] = pane.ID
	}
	return ids
}

func scanPanes(panes []herdr.Pane, scrolls map[string]herdr.Scroll) []scan.Pane {
	out := make([]scan.Pane, len(panes))
	for i, pane := range panes {
		size := marks.Content(pane, scrolls[pane.ID].ViewportRows)
		out[i] = scan.Pane{ID: pane.ID, Cols: size.Cols, Rows: size.Rows}
	}
	return out
}

func itemsFor(found []links.Link, codes []string) []ui.Item {
	items := make([]ui.Item, len(found))
	for i := range found {
		items[i] = ui.Item{Code: codes[i]}
	}
	return items
}

// firstBadges is every hint visible, nothing ruled out.
func firstBadges(found []links.Link, codes []string) map[string][]overlay.Badge {
	all := make([]int, len(found))
	for i := range all {
		all[i] = i
	}
	return hints.Badges(found, codes, all, "")
}

// narrowOpts redraws badges while narrowing, or leaves the pick a plain list.
func narrowOpts(ctx context.Context, marker *marks.Marker, found []links.Link, codes []string) ui.Options {
	opts := ui.Options{Alphabet: hints.DefaultAlphabet}
	if !marker.Live() {
		return opts
	}
	opts.OnNarrow = func(matches []int, typed string) {
		marker.Draw(ctx, hints.Badges(found, codes, matches, typed))
	}
	return opts
}
