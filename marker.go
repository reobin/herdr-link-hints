package main

import (
	"context"
	"log/slog"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/theme"
)

// marker owns the one graphics layer this plugin draws per pane. Herdr
// keeps a layer until it is cleared by name, past the exit of the process
// that set it.
type marker struct {
	client *herdr.Client
	log    *slog.Logger
	colors theme.Colors
	views  map[string]paneView
	drawn  map[string]bool
}

type paneView struct {
	cell overlay.Cell
	size overlay.Size
}

// newMarker keeps only the panes that can be drawn on. The graphics infos
// arrive pre-fetched: pick() gathers them alongside the link scan, so the
// marker build never serialises a round trip per pane.
func newMarker(client *herdr.Client, log *slog.Logger, colors theme.Colors, panes []herdr.Pane, scrolls map[string]herdr.Scroll, infos map[string]herdr.Graphics) *marker {
	views := make(map[string]paneView, len(panes))
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
			"cell_width_px", info.CellWidthPx, "cell_height_px", info.CellHeightPx)
		views[pane.ID] = paneView{
			cell: overlay.Cell{Width: info.CellWidthPx, Height: info.CellHeightPx},
			size: size,
		}
	}
	return &marker{client: client, log: log, colors: colors, views: views, drawn: map[string]bool{}}
}

// content strips the border off a pane's layout rect: the border is what
// the rect has that the viewport does not.
func content(pane herdr.Pane, viewportRows int) overlay.Size {
	if viewportRows <= 0 || viewportRows > pane.Height {
		return overlay.Size{Rows: pane.Height, Cols: pane.Width}
	}
	return overlay.Size{Rows: viewportRows, Cols: pane.Width - (pane.Height - viewportRows)}
}

// live reports whether there is anywhere to draw.
func (m *marker) live() bool { return m != nil && len(m.views) > 0 }

func (m *marker) draw(ctx context.Context, badges map[string][]overlay.Badge) {
	for pane, view := range m.views {
		frame, err := overlay.Render(overlay.Scene{
			Badges:   badges[pane],
			Colors:   m.colors,
			Cell:     view.cell,
			Viewport: view.size,
		})
		if err != nil {
			m.log.Debug("render overlay failed", "pane", pane, "error", err)
			m.clearPane(ctx, pane)
			continue
		}
		if err := m.client.SetGraphics(ctx, herdr.Frame{
			Pane:   pane,
			Layer:  overlay.LayerID,
			ZIndex: overlayZ,
			PNG:    frame.PNG,
			Width:  frame.Width,
			Height: frame.Height,
			Row:    frame.Row,
			Col:    frame.Col,
			Rows:   frame.Rows,
			Cols:   frame.Cols,
		}); err != nil {
			m.log.Debug("set overlay failed", "pane", pane, "error", err)
			m.clearPane(ctx, pane)
			continue
		}
		m.drawn[pane] = true
	}
}

func (m *marker) clear(ctx context.Context) {
	for pane := range m.views {
		m.clearPane(ctx, pane)
	}
}

func (m *marker) clearPane(ctx context.Context, pane string) {
	if !m.drawn[pane] {
		return
	}
	if err := m.client.ClearGraphics(ctx, pane, overlay.LayerID); err != nil {
		m.log.Debug("clear overlay failed", "pane", pane, "error", err)
		return
	}
	delete(m.drawn, pane)
}
