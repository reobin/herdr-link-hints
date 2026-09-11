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

// newMarker keeps only the panes that can be drawn on.
func newMarker(ctx context.Context, client *herdr.Client, log *slog.Logger, colors theme.Colors, panes []herdr.Pane, scrolls map[string]herdr.Scroll) *marker {
	views := make(map[string]paneView, len(panes))
	for _, pane := range panes {
		info, err := client.GraphicsInfo(ctx, pane.ID)
		if err != nil {
			log.Debug("graphics info failed", "pane", pane.ID, "error", err)
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
