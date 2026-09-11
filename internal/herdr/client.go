// Package herdr talks to a running Herdr server: pane inspection through
// the CLI, link activation over the control socket.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Sources accepted by `herdr pane read --source`.
const (
	SourceVisible   = "visible"
	SourceUnwrapped = "recent-unwrapped"
)

// Client holds no state between calls and starts nothing that outlives it.
type Client struct {
	bin        string
	socket     string
	cmdTimeout time.Duration
	rpcTimeout time.Duration
}

func New(opts ...Option) *Client {
	c := &Client{
		bin:        envOr("HERDR_BIN_PATH", "herdr"),
		socket:     envOr("HERDR_SOCKET_PATH", defaultSocket()),
		cmdTimeout: 15 * time.Second,
		rpcTimeout: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultSocket() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

// PaneLines reads a pane's text. A lines count of zero leaves the extent
// to Herdr.
func (c *Client) PaneLines(ctx context.Context, pane, source string, lines int) ([]string, error) {
	args := []string{"pane", "read", pane, "--source", source}
	if lines > 0 {
		args = append(args, "--lines", strconv.Itoa(lines))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(out), "\n"), nil
}

// Pane is a pane on screen. Width and Height are its outer rect in cells,
// border included.
type Pane struct {
	ID     string
	Width  int
	Height int
}

// ScreenPanes lists the panes sharing a screen with the given one, and
// falls back to that pane alone when the layout cannot be read.
func (c *Client) ScreenPanes(ctx context.Context, pane string) ([]Pane, error) {
	out, err := c.run(ctx, "pane", "layout", "--pane", pane)
	if err != nil {
		return []Pane{{ID: pane}}, err
	}
	return parseScreenPanes(out, pane)
}

func parseScreenPanes(out []byte, fallback string) ([]Pane, error) {
	var payload struct {
		Result struct {
			Layout struct {
				Panes []struct {
					PaneID string `json:"pane_id"`
					Rect   struct {
						Width  int `json:"width"`
						Height int `json:"height"`
					} `json:"rect"`
				} `json:"panes"`
			} `json:"layout"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return []Pane{{ID: fallback}}, fmt.Errorf("parse pane layout: %w", err)
	}
	var panes []Pane
	for _, p := range payload.Result.Layout.Panes {
		if p.PaneID != "" {
			panes = append(panes, Pane{ID: p.PaneID, Width: p.Rect.Width, Height: p.Rect.Height})
		}
	}
	if len(panes) == 0 {
		return []Pane{{ID: fallback}}, nil
	}
	return panes, nil
}

// PaneLabels gives every requested pane an entry, falling back to the tail
// of its ID.
func (c *Client) PaneLabels(ctx context.Context, panes []string) (map[string]string, error) {
	labels := defaultLabels(panes)
	out, err := c.run(ctx, "pane", "list")
	if err != nil {
		return labels, err
	}
	return parsePaneLabels(out, labels)
}

func defaultLabels(panes []string) map[string]string {
	labels := make(map[string]string, len(panes))
	for _, pane := range panes {
		labels[pane] = shortID(pane)
	}
	return labels
}

func parsePaneLabels(out []byte, labels map[string]string) (map[string]string, error) {
	var payload struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				Label  string `json:"label"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return labels, fmt.Errorf("parse pane list: %w", err)
	}
	for _, info := range payload.Result.Panes {
		if _, wanted := labels[info.PaneID]; wanted && info.Label != "" {
			labels[info.PaneID] = info.Label
		}
	}
	return labels, nil
}

func shortID(pane string) string {
	if i := strings.LastIndex(pane, ":"); i >= 0 {
		return pane[i+1:]
	}
	return pane
}

// Scroll is a pane's scroll state. Offset is how far scrollback reaches
// below the viewport.
type Scroll struct {
	Offset       int
	ViewportRows int
}

func (c *Client) PaneScroll(ctx context.Context, pane string) (Scroll, error) {
	out, err := c.run(ctx, "pane", "get", pane)
	if err != nil {
		return Scroll{}, err
	}
	return parsePaneScroll(out)
}

func parsePaneScroll(out []byte) (Scroll, error) {
	var payload struct {
		Result struct {
			Pane struct {
				Scroll struct {
					MaxOffsetFromBottom int `json:"max_offset_from_bottom"`
					ViewportRows        int `json:"viewport_rows"`
				} `json:"scroll"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return Scroll{}, fmt.Errorf("parse pane get: %w", err)
	}
	scroll := payload.Result.Pane.Scroll
	return Scroll{Offset: scroll.MaxOffsetFromBottom, ViewportRows: scroll.ViewportRows}, nil
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Stdin = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
