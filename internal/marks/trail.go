package marks

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const trailPrefix = "layers-"

// Trail records the layers a run has put up, so a run killed outright
// leaves the list for the next one. Clearing a layer already gone is a
// no-op, so an over-wide trail costs nothing.
type Trail struct {
	path string
	mu   sync.Mutex
	up   map[string][]string
}

// OpenTrail returns nil when there is nowhere to write; every call on nil is a no-op.
func OpenTrail(dir string) *Trail {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	return &Trail{
		path: filepath.Join(dir, trailPrefix+strconv.Itoa(os.Getpid())+".json"),
		up:   map[string][]string{},
	}
}

// mark notes a layer before it goes up, and only writes when it is new.
func (t *Trail) mark(pane, layer string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if slices.Contains(t.up[pane], layer) {
		return
	}
	t.up[pane] = append(t.up[pane], layer)
	body, err := json.Marshal(t.up)
	if err != nil {
		return
	}
	_ = os.WriteFile(t.path, body, 0o600)
}

// done drops the trail once the overlay is off the screen.
func (t *Trail) done() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.up = map[string][]string{}
	_ = os.Remove(t.path)
}

// Reclaim takes down what earlier runs left on screen. A new session makes
// any older overlay stale, so no liveness check is needed.
func Reclaim(ctx context.Context, client Painter, log *slog.Logger, dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	self := trailPrefix + strconv.Itoa(os.Getpid()) + ".json"
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, trailPrefix) || name == self {
			continue
		}
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var up map[string][]string
		if err := json.Unmarshal(body, &up); err != nil {
			_ = os.Remove(path)
			continue
		}
		for pane, layers := range up {
			for _, layer := range layers {
				if err := client.ClearGraphics(ctx, pane, layer); err != nil {
					log.Debug("reclaim failed", "pane", pane, "layer", layer, "error", err)
				}
			}
		}
		log.Debug("reclaimed a stranded overlay", "trail", name, "panes", len(up))
		_ = os.Remove(path)
	}
}
