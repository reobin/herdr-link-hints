// Command picker shows keyboard link hints for the panes on screen.
// Everything runs once per keypress and exits; nothing stays resident.
// Set HINTS_DEBUG=1 to log what each step did to stderr.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/reobin/herdr-link-hints/internal/browse"
	"github.com/reobin/herdr-link-hints/internal/demo"
	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/hints"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/overlay"
	"github.com/reobin/herdr-link-hints/internal/scan"
	"github.com/reobin/herdr-link-hints/internal/ui"
)

// Cancelling is distinct from failing so a key binding can tell the two
// apart.
const (
	exitOK        = 0
	exitFailed    = 1
	exitCancelled = 3
)

const (
	pluginID   = "herdr-link-hints"
	entrypoint = "picker"

	// envPlacement overrides the popup placement for testing.
	envPlacement = "HINTS_PLACEMENT"

	// overlayZ puts the hints above anything else a pane may have drawn.
	overlayZ = 1000
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 && args[0] == "--open" {
		return open()
	}
	if len(args) > 0 && args[0] == "--demo" {
		return runDemo()
	}
	return pick()
}

// open settles whether annotations are possible before opening the pane:
// a pane's shape is fixed once it opens.
func open() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := newLogger()
	focused := focusedPane()
	if focused == "" {
		log.Debug("could not resolve the focused pane")
		return exitFailed
	}

	client := herdr.New()
	cell, ok := graphicsCell(ctx, client, focused, log)
	if !ok {
		return exitFailed
	}
	open := paneFor(cell)
	pane, err := client.OpenPane(ctx, open)
	if err != nil {
		log.Debug("open picker pane failed", "placement", open.Placement, "error", err)
		return exitFailed
	}
	log.Debug("picker pane opened", "pane", pane,
		"placement", open.Placement, "width", open.Width, "height", open.Height)
	return exitOK
}

// paneFor picks the placement. Only a popup can be sized, and only a popup
// floats rather than reflowing the pane the hints are drawn on.
func paneFor(cell herdr.Graphics) herdr.PaneOpen {
	open := herdr.PaneOpen{
		Plugin:     pluginID,
		Entrypoint: entrypoint,
		Placement:  envOr(envPlacement, "popup"),
		Focus:      true,
		Env:        map[string]string{},
	}
	// The pane is a separate process, so debugging it needs the setting
	// carried across.
	if debug := os.Getenv("HINTS_DEBUG"); debug != "" {
		open.Env["HINTS_DEBUG"] = debug
	}
	if open.Placement == "popup" {
		open.Width, open.Height = paneShape(cell)
	}
	return open
}

// graphicsCell is the cell size that squares the popup. Without graphics
// there is nothing to draw on, so opening fails.
func graphicsCell(ctx context.Context, client *herdr.Client, pane string, log *slog.Logger) (herdr.Graphics, bool) {
	info, err := client.GraphicsInfo(ctx, pane)
	if err != nil {
		log.Debug("graphics unavailable", "pane", pane, "error", err)
		return info, false
	}
	log.Debug("graphics",
		"pane", pane,
		"cell_width_px", info.CellWidthPx,
		"cell_height_px", info.CellHeightPx,
		"pane_visible", info.PaneVisible,
		"max_layers_per_pane", info.MaxLayers)
	if !info.PaneVisible || info.CellWidthPx <= 0 || info.CellHeightPx <= 0 {
		return info, false
	}
	return info, true
}

// annotateRows is the outer height of the annotate popup. Herdr takes a
// border off each side, and four gives two content rows, which cannot
// centre a line; five gives three.
const annotateRows = 5

// paneShape keeps the annotate popup square on screen. Cells are far taller
// than they are wide, so squareness is a pixel measure, not a cell count.
func paneShape(cell herdr.Graphics) (width, height string) {
	width, height = strconv.Itoa(squareCols(cell)), strconv.Itoa(annotateRows)
	return envOr("HINTS_WIDTH", width), envOr("HINTS_HEIGHT", height)
}

// The fallback is a typical 2:1 cell, for a terminal that reported nothing.
func squareCols(cell herdr.Graphics) int {
	if cell.CellWidthPx <= 0 || cell.CellHeightPx <= 0 {
		return annotateRows * 2
	}
	return max(annotateRows*cell.CellHeightPx/cell.CellWidthPx, 1)
}

func runDemo() int {
	term := ui.Open(os.Stdin, os.Stdout)
	defer term.Close()

	found := demo.Ranked()
	codes := demo.Codes()
	var renderErr error
	opts := ui.Options{Alphabet: hints.DefaultAlphabet}
	opts.OnNarrow = func(matches []int, typed string) {
		badges := hints.Badges(found, codes, matches, typed)[demo.Pane]
		if _, err := overlay.Render(demo.Scene(badges)); err != nil {
			renderErr = err
		}
	}

	index, picked := ui.Pick(term, itemsFor(found, codes), opts)
	if renderErr != nil {
		term.Printf("\ndemo render failed: %v\n", renderErr)
		return exitFailed
	}
	if !picked {
		return exitCancelled
	}
	term.Printf("\nOpened %s in demo.\n", found[index].URL)
	return exitOK
}

func pick() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := newLogger()
	term := ui.Open(os.Stdin, os.Stdout)
	defer term.Close()
	rows, cols := term.Size()
	log.Debug("picker pane", "rows", rows, "cols", cols)

	focused := focusedPane()
	if focused == "" {
		term.Pause("no pane")
		return exitCancelled
	}

	client := herdr.New()
	scanning := term.Spin("scanning")
	panes, err := client.ScreenPanes(ctx, focused)
	if err != nil {
		log.Debug("pane layout failed", "pane", focused, "error", err)
	}
	ids := paneIDs(panes)
	// Must precede the snapshot it is the baseline for: sampled after, it
	// under-counts growth and Locate returns an unverified row.
	scrolls := paneScrolls(ctx, client, ids, log)

	scanner := &scan.Scanner{Source: client, Log: log, SkipObserve: os.Getenv("HINTS_NO_OBSERVE") != ""}
	scanInput := scanPanes(panes, scrolls)
	// The cursor sits where the prompt does: the viewport's bottom row.
	// That is also where the newest output is, so nearness to it and
	// recency pull the same way.
	cursors := make(map[string]int, len(scanInput))
	for _, pane := range scanInput {
		cursors[pane.ID] = pane.Rows - 1
	}
	// The graphics infos ride alongside the scan: a serial fetch would sit
	// on the critical path once the scan stops dominating it.
	var (
		infos   map[string]herdr.Graphics
		scanned []links.Link
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		infos = client.GraphicsInfos(ctx, ids)
	}()
	go func() {
		defer wg.Done()
		scanned = scanner.Links(ctx, scanInput)
	}()
	wg.Wait()
	scanning()

	found := hints.Rank(scanned, focused, cursors)

	marks := newMarker(client, log, term.Theme(), panes, scrolls, infos)
	// A layer Herdr has accepted outlives the process that set it.
	defer marks.clear(context.WithoutCancel(ctx))
	if len(found) == 0 {
		// The backdrop still goes up, so an empty screen reads as an answer.
		if marks.live() {
			marks.draw(ctx, nil)
		}
		term.Pause("no links")
		return exitCancelled
	}

	codes := hints.Codes(len(found), hints.DefaultAlphabet)
	opts := narrowOpts(ctx, marks, found, codes)

	index, picked := ui.Pick(term, itemsFor(found, codes), opts)
	if !picked {
		return exitCancelled
	}
	choice := found[index]

	row, col, located := scanner.Locate(ctx, choice, grownBy(ctx, client, choice.Pane, scrolls, log))
	if !located {
		term.Pause("scrolled")
		return exitFailed
	}
	// The pick is settled, so the screen comes back before the link opens
	// rather than after.
	if marks != nil {
		marks.clear(ctx)
	}

	url, handled := activate(ctx, client, choice, row, col, log)
	if handled {
		term.Printf("\nOpened %s via Herdr.\n", url)
		return exitOK
	}
	if err := browse.Open(url); err != nil {
		log.Debug("browser open failed", "url", url, "error", err)
		term.Pause("failed")
		return exitFailed
	}
	term.Printf("\nOpened %s in browser.\n", url)
	return exitOK
}

// focusedPane reads the env vars Herdr sets for the common case, then the
// JSON context blob for the rest.
func focusedPane() string {
	for _, key := range []string{"HERDR_ACTIVE_PANE_ID", "HERDR_PANE_ID"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	var pluginContext struct {
		FocusedPaneID string `json:"focused_pane_id"`
	}
	if raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &pluginContext)
	}
	return pluginContext.FocusedPaneID
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newLogger writes to HINTS_DEBUG when it names a file. The annotate pane is
// a few cells wide, so stderr there cannot be read.
func newLogger() *slog.Logger {
	target := os.Getenv("HINTS_DEBUG")
	if target == "" {
		return slog.New(slog.DiscardHandler)
	}
	out := io.Writer(os.Stderr)
	if strings.ContainsRune(target, os.PathSeparator) {
		file, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			out = file
		}
	}
	handler := slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(&durationHandler{Handler: handler, start: time.Now()})
}

// durationHandler stamps every record with how long the process has been
// running, so the HINTS_DEBUG lines measure startup instead of estimating
// it.
type durationHandler struct {
	slog.Handler
	start time.Time
}

func (h *durationHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.Int64("duration_ms", time.Since(h.start).Milliseconds()))
	return h.Handler.Handle(ctx, r)
}

func (h *durationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &durationHandler{Handler: h.Handler.WithAttrs(attrs), start: h.start}
}

func (h *durationHandler) WithGroup(name string) slog.Handler {
	return &durationHandler{Handler: h.Handler.WithGroup(name), start: h.start}
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
		size := content(pane, scrolls[pane.ID].ViewportRows)
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

// narrowOpts wires badge redraws while there is somewhere to draw. Without
// a graphics layer the pick continues as a code list: the count and echo
// readout needs no overlay.
func narrowOpts(ctx context.Context, marks *marker, found []links.Link, codes []string) ui.Options {
	opts := ui.Options{Alphabet: hints.DefaultAlphabet}
	if !marks.live() {
		return opts
	}
	opts.OnNarrow = func(matches []int, typed string) {
		marks.draw(ctx, hints.Badges(found, codes, matches, typed))
	}
	return opts
}

func paneScrolls(ctx context.Context, client *herdr.Client, panes []string, log *slog.Logger) map[string]herdr.Scroll {
	scrolls := make(map[string]herdr.Scroll, len(panes))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, pane := range panes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scroll, err := client.PaneScroll(ctx, pane)
			if err != nil {
				log.Debug("pane scroll failed", "pane", pane, "error", err)
			}
			mu.Lock()
			scrolls[pane] = scroll
			mu.Unlock()
		}()
	}
	wg.Wait()
	return scrolls
}

// grownBy reports how far the hints have scrolled upward.
func grownBy(ctx context.Context, client *herdr.Client, pane string, before map[string]herdr.Scroll, log *slog.Logger) int {
	now, err := client.PaneScroll(ctx, pane)
	if err != nil {
		log.Debug("pane scroll failed", "pane", pane, "error", err)
		return 0
	}
	return clampGrowth(now.Offset, before[pane].Offset)
}

// clampGrowth ignores a shrinking offset: the user scrolling back is not
// new output.
func clampGrowth(now, before int) int {
	if grown := now - before; grown > 0 {
		return grown
	}
	return 0
}

func activate(ctx context.Context, client *herdr.Client, choice links.Link, row, col int, log *slog.Logger) (string, bool) {
	result, err := client.ActivateLink(ctx, choice.Pane, row, col)
	if err != nil {
		log.Debug("activate failed", "pane", choice.Pane, "row", row, "col", col, "error", err)
	}
	return target(result, err, choice.URL)
}

// target trusts Herdr's resolved URL over the one we read off screen, but
// only a handled result counts as already opened.
func target(result herdr.Activation, err error, fallback string) (string, bool) {
	if err != nil {
		return fallback, false
	}
	url := fallback
	if result.URL != "" {
		url = result.URL
	}
	return url, result.Handled
}
