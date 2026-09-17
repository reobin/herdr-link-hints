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
	envHandoff = "HINTS_HANDOFF"
	// handoffTTL bounds how long a payload is trusted.
	handoffTTL = 5 * time.Second
)

// prepared is the whole scan, already done.
type prepared struct {
	Focused   string
	Panes     []herdr.Pane
	Scrolls   map[string]herdr.Scroll
	Infos     map[string]herdr.Graphics
	Colors    theme.Colors
	HasColors bool
	Found     []links.Link
	// Drawn names panes whose overlay is already up.
	Drawn   []string
	WroteAt time.Time
}

// gather reads layout, scroll baseline, and links.
func gather(ctx context.Context, client *herdr.Client, log *slog.Logger, focused string) prepared {
	// pane.list carries scroll, pane.layout none, so they run together.
	// Both precede the snapshot they baseline.
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

// writeHandoff leaves the payload for the pane process.
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

// readHandoff consumes the payload, false when unsure.
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

// sweepHandoffs drops payloads nobody came for.
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
