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
	"github.com/reobin/herdr-link-hints/internal/theme"
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
	// The dim backdrop goes under the frame and the still-matching badges
	// over it, so the three stack in the order they are drawn in.
	overlayZ = 1000
	dimZ     = overlayZ - 1
	badgeZ   = overlayZ + 1
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

// open does the whole scan and puts the hints up, then asks Herdr for the
// picker pane last. Nothing on the scan-render-set path needs a terminal,
// so opening last takes the pane spawn, its Go runtime start and its
// terminal setup off the wait before hints appear. It also removes the
// ordering hazard: the pane process cannot race a scan that already
// finished.
func open() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := newLogger()
	client := herdr.New(herdr.WithLogger(log))
	spec := paneFor()

	marks, ready := prepare(ctx, client, log, spec.Env)
	pane, err := client.OpenPane(ctx, spec)
	if err != nil {
		log.Debug("open picker pane failed", "placement", spec.Placement, "error", err)
		// Nothing will arrive to take the overlay down, so it comes down
		// here rather than staying on the user's screen.
		if ready {
			marks.clear(context.WithoutCancel(ctx))
		}
		return exitFailed
	}
	attrs := []any{"placement", spec.Placement, "width", spec.Width, "height", spec.Height}
	if pane != "" {
		attrs = append(attrs, "pane", pane)
	}
	log.Debug("picker pane opened", attrs...)
	return exitOK
}

// prepare scans, draws, and leaves the result where the picker pane will
// find it. A failure anywhere here is not fatal: the env key simply goes
// unset and the picker does the work itself, exactly as it used to.
func prepare(ctx context.Context, client *herdr.Client, log *slog.Logger, env map[string]string) (*marker, bool) {
	focused := focusedPane(log)
	if focused == "" {
		return nil, false
	}
	p := gather(ctx, client, log, focused)

	// Terminal.Theme gates its cache read behind having a tty, which this
	// process has not got, so the cache is read directly. Both processes
	// inherit TERM_PROGRAM from the server, so they resolve the same key.
	// On a miss nothing is drawn here and the picker draws after its own
	// probe, which is why a miss cannot flash the wrong colours.
	colors, cached := theme.Load(os.Getenv("TERM_PROGRAM"))
	p.Colors, p.HasColors = colors, cached

	var marks *marker
	if cached {
		marks = newMarker(client, log, colors, p.Panes, p.Scrolls, p.Infos)
		if marks.live() {
			marks.draw(ctx, firstBadges(p.Found, hints.Codes(len(p.Found), hints.DefaultAlphabet)))
			p.Drawn = marks.drawnPanes()
		}
	} else {
		log.Debug("theme cache miss, leaving the drawing to the picker pane")
	}

	path, err := writeHandoff(p)
	if err != nil {
		log.Debug("handoff not written, the picker will scan for itself", "error", err)
		return marks, marks != nil
	}
	env[envHandoff] = path
	return marks, marks != nil
}

// paneFor picks the placement. Only a popup can be sized, and only a popup
// floats rather than reflowing the pane the hints are drawn on.
func paneFor() herdr.PaneOpen {
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
		open.Width, open.Height = paneShape()
	}
	return open
}

// contentCols is the fixed content width of the picker popup. The readout's
// widest lines are the spinner with its label ("⠋ scanning", ten cells)
// and a three-digit count with its unit ("999 links", nine cells), so 22
// holds either with room for the centred padding that keeps the left edge
// still.
const contentCols = 22

// contentRows is the fixed content height of the picker popup. The readout
// is two rows, the echo above the count, and three content rows centre the
// count on the middle row; the spinner alone centres the same way.
const contentRows = 3

// paneShape is the fixed picker popup shape. Herdr numbers are outer
// dimensions, and it takes a border cell off each side with the required
// title drawn on it, so the content size grows by two each way.
func paneShape() (width, height string) {
	width, height = strconv.Itoa(contentCols+2), strconv.Itoa(contentRows+2)
	return envOr("HINTS_WIDTH", width), envOr("HINTS_HEIGHT", height)
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

	client := herdr.New(herdr.WithLogger(log))

	// The action process has usually done all of this already, and the
	// hints are on screen before this one started. When it has not, or its
	// answer is too old to trust, the scan happens here as it always did.
	p, handed := readHandoff(os.Getenv(envHandoff), log)
	if !handed {
		focused := focusedPane(log)
		if focused == "" {
			term.Pause("no pane")
			return exitCancelled
		}
		scanning := term.Spin("scanning")
		p = gather(ctx, client, log, focused)
		scanning()
	}
	log.Debug("picker input", "pane", p.Focused, "links", len(p.Found), "from_action", handed)

	colors := p.Colors
	if !p.HasColors {
		colors = term.Theme()
	}
	found := p.Found

	codes := hints.Codes(len(found), hints.DefaultAlphabet)
	marks := newMarker(client, log, colors, p.Panes, p.Scrolls, p.Infos)
	// A layer Herdr has accepted outlives the process that set it.
	defer marks.clear(context.WithoutCancel(ctx))
	// Whatever the action process put up stays up: drawing again would
	// re-encode a frame already on screen.
	marks.adopt(p.Drawn, firstBadges(found, codes))
	// The backdrop the first keystroke narrows against is built here, off
	// the path, while the user is still reading the hints.
	if marks.live() && len(found) > 0 {
		go marks.prime(ctx, firstBadges(found, codes))
	}
	if len(found) == 0 {
		// The backdrop still goes up, so an empty screen reads as an answer.
		if marks.live() {
			marks.draw(ctx, nil)
		}
		term.Pause("no links")
		return exitCancelled
	}
	opts := narrowOpts(ctx, marks, found, codes)

	index, picked := ui.Pick(term, itemsFor(found, codes), opts)
	if !picked {
		return exitCancelled
	}
	choice := found[index]

	scanner := &scan.Scanner{Source: client, Log: log}
	row, col, located := scanner.Locate(ctx, choice, grownBy(ctx, client, choice.Pane, p.Scrolls, log))
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
// JSON context blob for the rest. The order is unchanged on purpose: the
// context blob is the authoritative answer in a pane process only if
// HERDR_PANE_ID there is the picker popup's own id, and nobody has observed
// that it is. Every candidate is logged so the next debug run settles it.
func focusedPane(log *slog.Logger) string {
	var pluginContext struct {
		FocusedPaneID string `json:"focused_pane_id"`
	}
	if raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &pluginContext)
	}
	sources := []struct{ name, value string }{
		{"HERDR_ACTIVE_PANE_ID", os.Getenv("HERDR_ACTIVE_PANE_ID")},
		{"HERDR_PANE_ID", os.Getenv("HERDR_PANE_ID")},
		{"HERDR_PLUGIN_CONTEXT_JSON.focused_pane_id", pluginContext.FocusedPaneID},
	}
	chosen, from := "", "none"
	for _, source := range sources {
		if chosen == "" && source.value != "" {
			chosen, from = source.value, source.name
		}
		log.Debug("focused pane candidate", "source", source.name, "value", source.value)
	}
	log.Debug("focused pane", "pane", chosen, "source", from)
	return chosen
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
// firstBadges is what the picker draws before a key is typed: every hint
// visible, nothing ruled out. It has to match what ui.Pick asks for on its
// first pass, or the action process's frame gets replaced by an identical
// one.
func firstBadges(found []links.Link, codes []string) map[string][]overlay.Badge {
	all := make([]int, len(found))
	for i := range all {
		all[i] = i
	}
	return hints.Badges(found, codes, all, "")
}

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

// scrollsFor takes the batched pane.list answer where it covers every pane
// and falls back to a pane.get each where it does not.
func scrollsFor(ctx context.Context, client *herdr.Client, ids []string, listed map[string]herdr.Scroll, log *slog.Logger) map[string]herdr.Scroll {
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
	log.Debug("pane list missed panes, reading them one by one", "panes", missing)
	for id, scroll := range paneScrolls(ctx, client, missing, log) {
		scrolls[id] = scroll
	}
	return scrolls
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
