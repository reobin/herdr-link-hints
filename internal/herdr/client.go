// Package herdr talks to a running Herdr server over the control socket,
// dialling fresh for each call: the server closes the connection after
// each response. It falls back to the CLI for pane inspection until
// socket parity is proven. ObserveOSC8 stays on the CLI: no live OSC 8
// sample exists to prove a socket snapshot carries the same targets at
// the same cells.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SourceVisible is the `herdr pane read --source` value scanning uses.
const SourceVisible = "visible"

// Client dials Herdr's control socket fresh for each call. It starts
// nothing that outlives it; pane inspection that cannot go over the
// socket falls back to the CLI.
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

// PaneLines reads a pane's visible text, leaving the extent to Herdr. It
// goes over the socket and falls back to the CLI until socket parity is
// proven.
func (c *Client) PaneLines(ctx context.Context, pane string) ([]string, error) {
	if text, err := c.paneLinesSocket(ctx, pane); err == nil {
		return text, nil
	}
	out, err := c.run(ctx, "pane", "read", pane, "--source", SourceVisible)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(out), "\n"), nil
}

func (c *Client) paneLinesSocket(ctx context.Context, pane string) ([]string, error) {
	params := map[string]any{"pane_id": pane, "source": SourceVisible, "format": "text"}
	var result struct {
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	if err := c.call(ctx, "pane.read", params, &result); err != nil {
		return nil, err
	}
	return strings.Split(result.Read.Text, "\n"), nil
}

// Pane is a pane on screen. Width and Height are its outer rect in cells,
// border included.
type Pane struct {
	ID     string
	Width  int
	Height int
}

// ScreenPanes lists the panes sharing a screen with the given one, and
// falls back to that pane alone when the layout cannot be read. It goes
// over the socket and falls back to the CLI until socket parity is proven.
func (c *Client) ScreenPanes(ctx context.Context, pane string) ([]Pane, error) {
	if panes, err := c.screenPanesSocket(ctx, pane); err == nil {
		return panes, nil
	}
	out, err := c.run(ctx, "pane", "layout", "--pane", pane)
	if err != nil {
		return []Pane{{ID: pane}}, err
	}
	return parseScreenPanes(out, pane)
}

func (c *Client) screenPanesSocket(ctx context.Context, pane string) ([]Pane, error) {
	var result struct {
		Layout layoutResult `json:"layout"`
	}
	if err := c.call(ctx, "pane.layout", map[string]any{"pane_id": pane}, &result); err != nil {
		return nil, err
	}
	return panesFromLayout(result.Layout, pane), nil
}

// layoutResult is the layout object both the CLI envelope and the socket
// result carry.
type layoutResult struct {
	Panes []layoutPane `json:"panes"`
}

type layoutPane struct {
	PaneID string `json:"pane_id"`
	Rect   struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"rect"`
}

func panesFromLayout(layout layoutResult, fallback string) []Pane {
	var panes []Pane
	for _, p := range layout.Panes {
		if p.PaneID != "" {
			panes = append(panes, Pane{ID: p.PaneID, Width: p.Rect.Width, Height: p.Rect.Height})
		}
	}
	if len(panes) == 0 {
		return []Pane{{ID: fallback}}
	}
	return panes
}

func parseScreenPanes(out []byte, fallback string) ([]Pane, error) {
	var payload struct {
		Result struct {
			Layout layoutResult `json:"layout"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return []Pane{{ID: fallback}}, fmt.Errorf("parse pane layout: %w", err)
	}
	return panesFromLayout(payload.Result.Layout, fallback), nil
}

// PaneLabels gives every requested pane an entry, falling back to the tail
// of its ID. It goes over the socket and falls back to the CLI until socket
// parity is proven.
func (c *Client) PaneLabels(ctx context.Context, panes []string) (map[string]string, error) {
	labels := defaultLabels(panes)
	if listed, err := c.paneLabelsSocket(ctx, labels); err == nil {
		return listed, nil
	}
	out, err := c.run(ctx, "pane", "list")
	if err != nil {
		return labels, err
	}
	return parsePaneLabels(out, labels)
}

func (c *Client) paneLabelsSocket(ctx context.Context, labels map[string]string) (map[string]string, error) {
	var result struct {
		Panes []labelPane `json:"panes"`
	}
	if err := c.call(ctx, "pane.list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	fillLabels(labels, result.Panes)
	return labels, nil
}

type labelPane struct {
	PaneID string `json:"pane_id"`
	Label  string `json:"label"`
}

func fillLabels(labels map[string]string, panes []labelPane) {
	for _, info := range panes {
		if _, wanted := labels[info.PaneID]; wanted && info.Label != "" {
			labels[info.PaneID] = info.Label
		}
	}
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
			Panes []labelPane `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return labels, fmt.Errorf("parse pane list: %w", err)
	}
	fillLabels(labels, payload.Result.Panes)
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
	if scroll, err := c.paneScrollSocket(ctx, pane); err == nil {
		return scroll, nil
	}
	out, err := c.run(ctx, "pane", "get", pane)
	if err != nil {
		return Scroll{}, err
	}
	return parsePaneScroll(out)
}

func (c *Client) paneScrollSocket(ctx context.Context, pane string) (Scroll, error) {
	var result struct {
		Pane struct {
			Scroll scrollState `json:"scroll"`
		} `json:"pane"`
	}
	if err := c.call(ctx, "pane.get", map[string]any{"pane_id": pane}, &result); err != nil {
		return Scroll{}, err
	}
	return Scroll{Offset: result.Pane.Scroll.MaxOffsetFromBottom, ViewportRows: result.Pane.Scroll.ViewportRows}, nil
}

type scrollState struct {
	MaxOffsetFromBottom int `json:"max_offset_from_bottom"`
	ViewportRows        int `json:"viewport_rows"`
}

func parsePaneScroll(out []byte) (Scroll, error) {
	var payload struct {
		Result struct {
			Pane struct {
				Scroll scrollState `json:"scroll"`
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
