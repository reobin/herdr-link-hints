// Package handoff carries a finished scan from the action process to the
// picker pane, so the pane does not scan a second time.
package handoff

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/theme"
)

// EnvVar names the file on the picker pane's environment.
const EnvVar = "HINTS_HANDOFF"

// ttl bounds how long a payload is trusted.
const ttl = 5 * time.Second

// Payload is the whole scan, already done.
type Payload struct {
	Focused   string
	Panes     []herdr.Pane
	Scrolls   map[string]herdr.Scroll
	Infos     map[string]herdr.Graphics
	Colors    theme.Colors
	HasColors bool
	Found     []links.Link
	// Popup is where the picker popup will sit on the surface.
	Popup herdr.Rect
	// Drawn names the frame layers already up, per pane.
	Drawn   map[string][]string
	WroteAt time.Time
}

// Write leaves the payload in dir for the pane process.
func Write(dir string, p Payload) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no state directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p.WroteAt = time.Now()
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sweep(dir)
	path := filepath.Join(dir, fmt.Sprintf("handoff-%d.json", os.Getpid()))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Read consumes the payload, false when unsure.
func Read(path string, log *slog.Logger) (Payload, bool) {
	if path == "" {
		return Payload{}, false
	}
	defer func() { _ = os.Remove(path) }()

	body, err := os.ReadFile(path)
	if err != nil {
		log.Debug("handoff unreadable", "path", path, "error", err)
		return Payload{}, false
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Debug("handoff unparseable", "path", path, "error", err)
		return Payload{}, false
	}
	if age := time.Since(p.WroteAt); age > ttl {
		log.Debug("handoff stale", "path", path, "age", age)
		return Payload{}, false
	}
	if p.Focused == "" {
		return Payload{}, false
	}
	return p, true
}

// sweep drops payloads nobody came for.
func sweep(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "handoff-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) <= ttl {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
