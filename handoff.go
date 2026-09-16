package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/hints"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/scan"
	"github.com/reobin/herdr-link-hints/internal/theme"
)

const (
	// envHandoff names the file the action process left its work in.
	envHandoff = "HINTS_HANDOFF"
	// handoffTTL is how long a payload is worth reading. The pane process
	// starts milliseconds after it is written, so anything older belongs to
	// a run that died.
	handoffTTL = 5 * time.Second
)

// prepared is the whole scan, already done. Nothing on the path that
// produces it needs a terminal, so the action process can do it before the
// picker pane exists and the pane spawn leaves the visible path.
type prepared struct {
	Focused   string
	Panes     []herdr.Pane
	Scrolls   map[string]herdr.Scroll
	Infos     map[string]herdr.Graphics
	Colors    theme.Colors
	HasColors bool
	Found     []links.Link
	// Drawn names the panes whose overlay is already on screen, so the
	// picker does not re-encode a frame that is already up.
	Drawn   []string
	WroteAt time.Time
}

// gather reads the layout, the scroll baseline and the links. It is the
// same work in either process; only where it runs changes.
func gather(ctx context.Context, client *herdr.Client, log *slog.Logger, focused string) prepared {
	// pane.list carries every pane's scroll and pane.layout carries none,
	// so the two run together and the scroll cost stays flat in pane count.
	// Both must precede the snapshot they are the baseline for: sampled
	// after, the scroll under-counts growth and Locate returns an
	// unverified row.
	var (
		panes   []herdr.Pane
		listed  map[string]herdr.Scroll
		layoutW sync.WaitGroup
	)
	layoutW.Add(2)
	go func() {
		defer layoutW.Done()
		var err error
		if panes, err = client.ScreenPanes(ctx, focused); err != nil {
			log.Debug("pane layout failed", "pane", focused, "error", err)
		}
	}()
	go func() {
		defer layoutW.Done()
		var err error
		if listed, err = client.PaneScrolls(ctx); err != nil {
			log.Debug("pane list failed", "error", err)
		}
	}()
	layoutW.Wait()

	ids := paneIDs(panes)
	scrolls := scrollsFor(ctx, client, ids, listed, log)

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

	return prepared{
		Focused: focused,
		Panes:   panes,
		Scrolls: scrolls,
		Infos:   infos,
		Found:   hints.Rank(scanned, focused, cursors),
	}
}

// writeHandoff leaves the payload where the pane process can pick it up.
// The state dir is Herdr's, and both processes inherit it.
func writeHandoff(p prepared) (string, error) {
	dir := os.Getenv("HERDR_PLUGIN_STATE_DIR")
	if dir == "" {
		return "", fmt.Errorf("HERDR_PLUGIN_STATE_DIR unset")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p.WroteAt = time.Now()
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sweepHandoffs(dir)
	path := filepath.Join(dir, fmt.Sprintf("handoff-%d.json", os.Getpid()))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// readHandoff consumes the payload, and reports false for anything it is
// not sure of so the picker falls back to scanning for itself.
func readHandoff(path string, log *slog.Logger) (prepared, bool) {
	if path == "" {
		return prepared{}, false
	}
	defer func() { _ = os.Remove(path) }()

	body, err := os.ReadFile(path)
	if err != nil {
		log.Debug("handoff unreadable", "path", path, "error", err)
		return prepared{}, false
	}
	var p prepared
	if err := json.Unmarshal(body, &p); err != nil {
		log.Debug("handoff unparseable", "path", path, "error", err)
		return prepared{}, false
	}
	if age := time.Since(p.WroteAt); age > handoffTTL {
		log.Debug("handoff stale", "path", path, "age", age)
		return prepared{}, false
	}
	if p.Focused == "" {
		return prepared{}, false
	}
	return p, true
}

// sweepHandoffs drops payloads nobody came for: a run whose picker pane
// never opened leaves its file behind, and the state dir is Herdr's.
func sweepHandoffs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "handoff-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) <= handoffTTL {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
