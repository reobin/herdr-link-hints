package main

import (
	"context"
	"log/slog"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/theme"
)

// marker owns the one graphics layer this plugin draws on the focused
// pane. Herdr keeps a layer until it is cleared by name, past the exit of
// the process that set it.
type marker struct {
	client *herdr.Client
	log    *slog.Logger
	colors theme.Colors
	pane   string
	cell   overlay.Cell
	size   overlay.Size
	drawn  bool
}

// newMarker returns a marker with nothing to draw on when the pane cannot
// take a layer, which is what sends the picker to its no-layer path.
func newMarker(ctx context.Context, client *herdr.Client, log *slog.Logger, colors theme.Colors, pane herdr.Pane, size overlay.Size) *marker {
	m := &marker{client: client, log: log, colors: colors}
	info, err := client.GraphicsInfo(ctx, pane.ID)
	if err != nil {
		log.Debug("graphics info failed", "pane", pane.ID, "error", err)
		return m
	}
	if !info.PaneVisible || info.CellWidthPx <= 0 || info.CellHeightPx <= 0 {
		log.Debug("pane cannot be drawn on", "pane", pane.ID, "visible", info.PaneVisible)
		return m
	}
	// A rect Herdr would not give up leaves nowhere to put the scrim.
	if size.Rows <= 0 || size.Cols <= 0 {
		log.Debug("pane viewport is unknown", "pane", pane.ID, "rows", size.Rows, "cols", size.Cols)
		return m
	}
	log.Debug("pane viewport", "pane", pane.ID, "rows", size.Rows, "cols", size.Cols,
		"cell_width_px", info.CellWidthPx, "cell_height_px", info.CellHeightPx)
	m.pane = pane.ID
	m.cell = overlay.Cell{Width: info.CellWidthPx, Height: info.CellHeightPx}
	m.size = size
	return m
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
func (m *marker) live() bool { return m != nil && m.pane != "" }

func (m *marker) draw(ctx context.Context, badges []overlay.Badge) {
	frame, err := overlay.Render(overlay.Scene{
		Badges:   badges,
		Colors:   m.colors,
		Cell:     m.cell,
		Viewport: m.size,
	})
	if err != nil {
		m.log.Debug("render overlay failed", "pane", m.pane, "error", err)
		m.clear(ctx)
		return
	}
	if err := m.client.SetGraphics(ctx, herdr.Frame{
		Pane:   m.pane,
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
		m.log.Debug("set overlay failed", "pane", m.pane, "error", err)
		m.clear(ctx)
		return
	}
	m.drawn = true
}

func (m *marker) clear(ctx context.Context) {
	if !m.drawn {
		return
	}
	if err := m.client.ClearGraphics(ctx, m.pane, overlay.LayerID); err != nil {
		m.log.Debug("clear overlay failed", "pane", m.pane, "error", err)
		return
	}
	m.drawn = false
}
