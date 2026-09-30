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
	"github.com/reobin/herdr-link-hints/internal/scan"
	"github.com/reobin/herdr-link-hints/internal/ui"
)

// client is the Herdr slice the two flows use. *herdr.Client satisfies it;
// tests supply a fake.
type client interface {
	scan.Source
	ScreenPanes(ctx context.Context, pane string) (herdr.Layout, error)
	PaneScrolls(ctx context.Context) (map[string]herdr.Scroll, error)
	PaneScroll(ctx context.Context, pane string) (herdr.Scroll, error)
	ActivateLink(ctx context.Context, pane string, row, col int) (herdr.Activation, error)
	OpenPane(ctx context.Context, p herdr.PaneOpen) (string, error)
}

// app carries what both flows need, injected rather than read from the environment.
type app struct {
	cfg    config.Config
	log    *slog.Logger
	client client
}

// open scans and hands the result to the picker pane.
func (a *app) open(ctx context.Context) int {
	spec := a.paneSpec()
	a.prepare(ctx, spec.Env)
	pane, err := a.client.OpenPane(ctx, spec)
	if err != nil {
		a.log.Debug("open picker pane failed", "placement", spec.Placement, "error", err)
		return exitFailed
	}
	attrs := []any{"placement", spec.Placement, "width", spec.Width, "height", spec.Height}
	if pane != "" {
		attrs = append(attrs, "pane", pane)
	}
	a.log.Debug("picker pane opened", attrs...)
	return exitOK
}

// prepare scans and leaves the result for the picker pane.
// Failures fall back to scanning in the picker.
func (a *app) prepare(ctx context.Context, env map[string]string) {
	focused := a.focusedPane()
	if focused == "" {
		return
	}
	p := a.gather(ctx, focused)

	path, err := handoff.Write(a.cfg.StateDir, p)
	if err != nil {
		a.log.Debug("handoff not written, the picker will scan for itself", "error", err)
		return
	}
	env[handoff.EnvVar] = path
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

func (a *app) pick(ctx context.Context, term *ui.Terminal) int {
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

	found := p.Found
	if len(found) == 0 {
		term.Pause("no links")
		return exitCancelled
	}

	codes := hints.Codes(len(found), hints.DefaultAlphabet)
	index, picked := ui.Pick(term, itemsFor(found, codes), ui.Options{Alphabet: hints.DefaultAlphabet})
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
	return handoff.Payload{
		Focused: focused,
		Panes:   panes,
		Scrolls: scrolls,
		Found:   links.Uniq(hints.Rank(scanner.Links(ctx, scanPanes(panes, scrolls)), focused, cursors(panes, scrolls))),
	}
}

// cursors sit at the viewport bottom, where the newest output is.
func cursors(panes []herdr.Pane, scrolls map[string]herdr.Scroll) map[string]int {
	cursors := make(map[string]int, len(panes))
	for _, pane := range panes {
		_, rows := contentSize(pane, scrolls[pane.ID].ViewportRows)
		cursors[pane.ID] = rows - 1
	}
	return cursors
}

// contentSize strips the border off a pane's rect.
func contentSize(pane herdr.Pane, viewportRows int) (cols, rows int) {
	if viewportRows <= 0 || viewportRows > pane.Height {
		return pane.Width, pane.Height
	}
	return pane.Width - (pane.Height - viewportRows), viewportRows
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
		cols, rows := contentSize(pane, scrolls[pane.ID].ViewportRows)
		out[i] = scan.Pane{ID: pane.ID, Cols: cols, Rows: rows}
	}
	return out
}

// itemsFor carries each link into the list: code and target, nothing
// else. Repeats are already uniqified, so every row is distinct.
func itemsFor(found []links.Link, codes []string) []ui.Item {
	items := make([]ui.Item, len(found))
	for i := range found {
		items[i] = ui.Item{
			Code: codes[i],
			URL:  found[i].URL,
		}
	}
	return items
}
