// Package herdr talks to a running Herdr server over the control socket.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SourceVisible is the `herdr pane read --source` value scanning uses.
const SourceVisible = "visible"

// Client dials Herdr's control socket fresh for each call.
type Client struct {
	bin        string
	socket     string
	cmdTimeout time.Duration
	rpcTimeout time.Duration
	log        *slog.Logger
	sendMu     sync.Mutex
}

func New(opts ...Option) *Client {
	c := &Client{
		bin:        envOr("HERDR_BIN_PATH", "herdr"),
		socket:     envOr("HERDR_SOCKET_PATH", defaultSocket()),
		cmdTimeout: 15 * time.Second,
		rpcTimeout: 5 * time.Second,
		log:        slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(c)
	}
	// Resolving via PATH costs ~100ms against ~6ms for the binary.
	if os.Getenv("HERDR_BIN_PATH") == "" {
		c.log.Debug("HERDR_BIN_PATH unset, resolving herdr through PATH", "bin", c.bin)
	}
	return c
}

// fellBack records a socket error papered over by the CLI.
func (c *Client) fellBack(method string, err error) {
	c.log.Debug("socket call failed, falling back to the CLI", "method", method, "error", err)
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

// PaneLines reads a pane's visible text.
func (c *Client) PaneLines(ctx context.Context, pane string) ([]string, error) {
	text, err := c.paneLinesSocket(ctx, pane)
	if err == nil {
		return text, nil
	}
	c.fellBack("pane.read", err)
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

// Pane is a pane on screen. Width and Height include the border.
type Pane struct {
	ID     string
	Width  int
	Height int
}

// ScreenPanes lists panes sharing a screen, else the pane alone.
func (c *Client) ScreenPanes(ctx context.Context, pane string) ([]Pane, error) {
	panes, err := c.screenPanesSocket(ctx, pane)
	if err == nil {
		return panes, nil
	}
	c.fellBack("pane.layout", err)
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

// layoutResult is the layout object CLI and socket share.
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

type listedPane struct {
	PaneID string      `json:"pane_id"`
	Scroll scrollState `json:"scroll"`
}

// Scroll is a pane's scroll state. Offset is scrollback below the viewport.
type Scroll struct {
	Offset       int
	ViewportRows int
}

// PaneScrolls reads every pane's scroll from one pane.list.
func (c *Client) PaneScrolls(ctx context.Context) (map[string]Scroll, error) {
	var result struct {
		Panes []listedPane `json:"panes"`
	}
	if err := c.call(ctx, "pane.list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	scrolls := make(map[string]Scroll, len(result.Panes))
	for _, pane := range result.Panes {
		if pane.PaneID != "" {
			scrolls[pane.PaneID] = pane.Scroll.scroll()
		}
	}
	return scrolls, nil
}

func (c *Client) PaneScroll(ctx context.Context, pane string) (Scroll, error) {
	scroll, err := c.paneScrollSocket(ctx, pane)
	if err == nil {
		return scroll, nil
	}
	c.fellBack("pane.get", err)
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
	return result.Pane.Scroll.scroll(), nil
}

type scrollState struct {
	MaxOffsetFromBottom int `json:"max_offset_from_bottom"`
	ViewportRows        int `json:"viewport_rows"`
}

func (s scrollState) scroll() Scroll {
	return Scroll{Offset: s.MaxOffsetFromBottom, ViewportRows: s.ViewportRows}
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
	return payload.Result.Pane.Scroll.scroll(), nil
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
