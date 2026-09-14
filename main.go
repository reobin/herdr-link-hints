// Command picker shows keyboard link hints for the panes on screen.
// Everything runs once per keypress and exits; nothing stays resident.
// Set HINTS_DEBUG=1 to log what each step did to stderr.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/reobin/herdr-link-hints/internal/browse"
	"github.com/reobin/herdr-link-hints/internal/cells"
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

	// envMode carries the --open decision into the pane: --open can reach the
	// socket but has no terminal.
	envMode      = "HINTS_MODE"
	envPlacement = "HINTS_PLACEMENT"
	modeAnnotate = "annotate"
	modeList     = "list"

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
	mode := pickMode(ctx, client, focused, log)
	open := paneFor(mode)
	pane, err := client.OpenPane(ctx, open)
	if err != nil {
		log.Debug("open picker pane failed", "placement", open.Placement, "error", err)
		return exitFailed
	}
	log.Debug("picker pane opened", "pane", pane, "mode", mode,
		"placement", open.Placement, "width", open.Width, "height", open.Height)
	return exitOK
}

// paneFor picks the placement. Only a popup can be sized, and only a popup
// floats rather than reflowing the pane the hints are drawn on.
func paneFor(mode string) herdr.PaneOpen {
	open := herdr.PaneOpen{
		Plugin:     pluginID,
		Entrypoint: entrypoint,
		Placement:  envOr(envPlacement, "popup"),
		Focus:      true,
		Env:        map[string]string{envMode: mode},
	}
	if open.Placement == "popup" {
		open.Width, open.Height = paneShape(mode)
	}
	return open
}

// pickMode falls back to the list when there are no graphics to draw on.
func pickMode(ctx context.Context, client *herdr.Client, pane string, log *slog.Logger) string {
	info, err := client.GraphicsInfo(ctx, pane)
	if err != nil {
		log.Debug("graphics unavailable", "pane", pane, "error", err)
		return modeList
	}
	log.Debug("graphics",
		"pane", pane,
		"cell_width_px", info.CellWidthPx,
		"cell_height_px", info.CellHeightPx,
		"pane_visible", info.PaneVisible,
		"max_layers_per_pane", info.MaxLayers)
	if !info.PaneVisible || info.CellWidthPx <= 0 || info.CellHeightPx <= 0 {
		return modeList
	}
	return modeAnnotate
}

// paneShape sizes the annotate popup for three rows of content. Herdr
// floors the outer height at four, and 2, 3 and 4 all give two content
// rows, which cannot centre a line.
func paneShape(mode string) (width, height string) {
	width, height = "80%", "60%"
	if mode == modeAnnotate {
		width, height = "22", "5"
	}
	return envOr("HINTS_WIDTH", width), envOr("HINTS_HEIGHT", height)
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
		term.Pause("no pane to hint")
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

	var (
		labels map[string]string
		found  []links.Link
		wg     sync.WaitGroup
	)
	scanner := &scan.Scanner{Source: client, Log: log, SkipObserve: os.Getenv("HINTS_NO_OBSERVE") != ""}
	wg.Add(2)
	go func() {
		defer wg.Done()
		var err error
		labels, err = client.PaneLabels(ctx, ids)
		if err != nil {
			log.Debug("pane list failed", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		found = scanner.Links(ctx, scanPanes(panes, scrolls))
	}()
	wg.Wait()
	scanning()

	title := screenTitle(ids, labels, focused)

	var marks *marker
	if os.Getenv(envMode) == modeAnnotate {
		marks = newMarker(ctx, client, log, term.Theme(), panes, scrolls)
		// A layer Herdr has accepted outlives the process that set it.
		defer marks.clear(context.WithoutCancel(ctx))
	}
	if len(found) == 0 {
		// The backdrop still goes up, so an empty screen reads as an answer.
		if marks != nil {
			marks.draw(ctx, nil)
		}
		term.Pause("no links on screen")
		return exitCancelled
	}

	codes := hints.Codes(len(found), hints.DefaultAlphabet)
	opts := ui.Options{Title: title, Alphabet: hints.DefaultAlphabet}
	if marks != nil {
		if len(marks.views) == 0 {
			term.Pause("nothing to draw on")
			return exitFailed
		}
		opts.Style = ui.StyleStatus
		opts.OnNarrow = func(matches []int, _ string) {
			marks.draw(ctx, badgesFor(found, codes, matches))
		}
	}

	index, picked := ui.Pick(term, itemsFor(found, codes, labels, len(ids) > 1), opts)
	if !picked {
		return exitCancelled
	}
	choice := found[index]

	row, col, located := scanner.Locate(ctx, choice, grownBy(ctx, client, choice.Pane, scrolls, log))
	if !located {
		term.Pause("scrolled off screen")
		return exitFailed
	}

	url, handled := activate(ctx, client, choice, row, col, log)
	if handled {
		term.Printf("\nOpened %s via Herdr.\n", url)
		return exitOK
	}
	if err := browse.Open(url); err != nil {
		log.Debug("browser open failed", "url", url, "error", err)
		term.Pause("could not open it")
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

func newLogger() *slog.Logger {
	if os.Getenv("HINTS_DEBUG") == "" {
		return slog.New(slog.DiscardHandler)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
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

func screenTitle(panes []string, labels map[string]string, focused string) string {
	if len(panes) > 1 {
		return fmt.Sprintf("%d panes", len(panes))
	}
	if label := labels[focused]; label != "" {
		return "pane " + label
	}
	return "pane " + focused
}

func itemsFor(found []links.Link, codes []string, labels map[string]string, showPane bool) []ui.Item {
	items := make([]ui.Item, len(found))
	for i, link := range found {
		where := ""
		if showPane {
			if where = labels[link.Pane]; where == "" {
				where = link.Pane
			}
		}
		items[i] = ui.Item{Code: codes[i], Text: link.Text, Where: where, Row: link.Row}
	}
	return items
}

// badgesFor dims the links a prefix has ruled out rather than removing
// them, so narrowing does not rearrange the screen.
func badgesFor(found []links.Link, codes []string, matches []int) map[string][]overlay.Badge {
	matched := make(map[int]bool, len(matches))
	for _, i := range matches {
		matched[i] = true
	}
	badges := make(map[string][]overlay.Badge, len(found))
	for i, link := range found {
		badges[link.Pane] = append(badges[link.Pane], overlay.Badge{
			Row:    link.Row,
			Col:    link.Col,
			Before: link.Before,
			Width:  cells.Width(link.Text),
			Code:   codes[i],
			Dim:    !matched[i],
		})
	}
	return badges
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
