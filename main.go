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
	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/hints"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/scan"
	"github.com/reobin/herdr-link-hints/internal/ui"
)

// Cancelling is kept distinct from failing so a key binding can tell "the
// user changed their mind" from "the plugin broke".
const (
	exitOK        = 0
	exitFailed    = 1
	exitCancelled = 3
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := newLogger()
	term := ui.Open(os.Stdin, os.Stdout)
	defer term.Close()

	focused := focusedPane()
	if focused == "" {
		term.Pause("Link hints: could not resolve the focused pane.")
		return exitCancelled
	}

	client := herdr.New()
	panes, err := client.ScreenPanes(ctx, focused)
	if err != nil {
		log.Debug("pane layout failed", "pane", focused, "error", err)
	}

	term.Clear()
	term.Printf("Scanning %s for links...\n", provisionalTitle(panes, focused))
	term.Flush()

	var (
		labels map[string]string
		found  []links.Link
		wg     sync.WaitGroup
	)
	scanner := &scan.Scanner{Source: client, Log: log, SkipObserve: os.Getenv("HINTS_NO_OBSERVE") != ""}
	// Must precede the snapshot it is compared against: sampled after, it
	// under-counts growth and Locate returns an unverified row.
	offsets := scrollOffsets(ctx, client, panes, log)
	wg.Add(2)
	go func() {
		defer wg.Done()
		var err error
		labels, err = client.PaneLabels(ctx, panes)
		if err != nil {
			log.Debug("pane list failed", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		found = scanner.Links(ctx, panes)
	}()
	wg.Wait()

	title := screenTitle(panes, labels, focused)
	if len(found) == 0 {
		term.Pause(fmt.Sprintf("Link hints: no links on screen (%s).", title))
		return exitCancelled
	}

	index, picked := ui.Pick(term, itemsFor(found, labels, len(panes) > 1), ui.Options{
		Title:    title,
		Alphabet: hints.DefaultAlphabet,
	})
	if !picked {
		return exitCancelled
	}
	choice := found[index]

	row, col, located := scanner.Locate(ctx, choice, grownBy(ctx, client, choice.Pane, offsets, log))
	if !located {
		term.Pause(fmt.Sprintf("%s scrolled off screen.", choice.URL))
		return exitFailed
	}

	url, handled := activate(ctx, client, choice, row, col, log)
	if handled {
		term.Printf("\nOpened %s via Herdr.\n", url)
		return exitOK
	}
	if err := browse.Open(url); err != nil {
		log.Debug("browser open failed", "url", url, "error", err)
		term.Pause(fmt.Sprintf("\nCould not open %s.", url))
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

func newLogger() *slog.Logger {
	if os.Getenv("HINTS_DEBUG") == "" {
		return slog.New(slog.DiscardHandler)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func provisionalTitle(panes []string, focused string) string {
	if len(panes) > 1 {
		return fmt.Sprintf("%d panes", len(panes))
	}
	return "pane " + focused
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

func itemsFor(found []links.Link, labels map[string]string, showPane bool) []ui.Item {
	codes := hints.Codes(len(found), hints.DefaultAlphabet)
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

func scrollOffsets(ctx context.Context, client *herdr.Client, panes []string, log *slog.Logger) map[string]int {
	offsets := make(map[string]int, len(panes))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, pane := range panes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			offset, err := client.ScrollOffset(ctx, pane)
			if err != nil {
				log.Debug("scroll offset failed", "pane", pane, "error", err)
			}
			mu.Lock()
			offsets[pane] = offset
			mu.Unlock()
		}()
	}
	wg.Wait()
	return offsets
}

// grownBy reports how many lines of new output arrived while the user was
// picking, which is how far the hints have scrolled upward.
func grownBy(ctx context.Context, client *herdr.Client, pane string, before map[string]int, log *slog.Logger) int {
	now, err := client.ScrollOffset(ctx, pane)
	if err != nil {
		log.Debug("scroll offset failed", "pane", pane, "error", err)
		return 0
	}
	return clampGrowth(now, before[pane])
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
