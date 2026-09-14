package herdr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Activation reports Handled only when a configured link handler actually
// opened the target; otherwise opening is left to the caller.
type Activation struct {
	URL     string `json:"url"`
	Handled bool   `json:"handled"`
}

// ActivateLink goes through Herdr so OSC 8 targets and custom handlers
// resolve the way a click would.
func (c *Client) ActivateLink(ctx context.Context, pane string, row, col int) (Activation, error) {
	var result Activation
	params := map[string]any{"pane_id": pane, "viewport_row": row, "col": col}
	err := c.call(ctx, "pane.link.activate", params, &result)
	return result, err
}

type rpcRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type rpcResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// Code is Herdr's error code, a string rather than the number it looks like.
type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("herdr rpc error %s: %s", e.Code, e.Message)
}

var requestSeq atomic.Uint64

func nextRequestID() string {
	return fmt.Sprintf("link-hints-%d-%d", os.Getpid(), requestSeq.Add(1))
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	// The server closes the connection after each response, so every
	// call dials fresh: reusing a connection fails the next write with
	// a broken pipe.
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	id := nextRequestID()
	body, err := json.Marshal(rpcRequest{ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("send %s request: %w", method, err)
	}

	// The socket also carries events and other clients' replies.
	decoder := json.NewDecoder(conn)
	for {
		var response rpcResponse
		if err := decoder.Decode(&response); err != nil {
			return fmt.Errorf("read %s response: %w", method, err)
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return response.Error
		}
		if result == nil || len(response.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("parse %s result: %w", method, err)
		}
		return nil
	}
}

// dial opens one connection for a single call.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("dial herdr socket %s: %w", c.socket, err)
	}
	return conn, nil
}

// Graphics reports what pane.graphics can do for a pane. A feature_disabled
// error instead means the outer terminal has no Kitty graphics support,
// which is the signal to fall back to the list picker.
type Graphics struct {
	CellWidthPx  int  `json:"cell_width_px"`
	CellHeightPx int  `json:"cell_height_px"`
	PaneVisible  bool `json:"pane_visible"`
	MaxLayers    int  `json:"max_layers_per_pane"`
}

func (c *Client) GraphicsInfo(ctx context.Context, pane string) (Graphics, error) {
	var result Graphics
	err := c.call(ctx, "pane.graphics.info", map[string]any{"pane_id": pane}, &result)
	return result, err
}

// GraphicsInfos fetches every pane's graphics info concurrently and keeps
// only the successes: the marker build runs alongside the link scan, so a
// serial fetch would sit on the critical path once the scan stops
// dominating it. A pane with no entry is one that cannot be drawn on.
func (c *Client) GraphicsInfos(ctx context.Context, panes []string) map[string]Graphics {
	infos := make(map[string]Graphics, len(panes))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, pane := range panes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := c.GraphicsInfo(ctx, pane)
			if err != nil {
				return
			}
			mu.Lock()
			infos[pane] = info
			mu.Unlock()
		}()
	}
	wg.Wait()
	return infos
}

// Frame is one image placed over a pane's viewport cells.
type Frame struct {
	Pane   string
	Layer  string
	ZIndex int
	PNG    []byte
	Width  int // pixels
	Height int // pixels
	Row    int // cells
	Col    int
	Rows   int
	Cols   int
}

func (c *Client) SetGraphics(ctx context.Context, f Frame) error {
	params := map[string]any{
		"pane_id":      f.Pane,
		"layer_id":     f.Layer,
		"z_index":      f.ZIndex,
		"format":       "png",
		"image_width":  f.Width,
		"image_height": f.Height,
		"data_base64":  base64.StdEncoding.EncodeToString(f.PNG),
		"placement": map[string]any{
			"viewport_row": f.Row,
			"viewport_col": f.Col,
			"grid_rows":    f.Rows,
			"grid_cols":    f.Cols,
		},
	}
	return c.call(ctx, "pane.graphics.set", params, nil)
}

// ClearGraphics names the layer: omitting it would clear only the layer
// Herdr calls "primary", not ours.
func (c *Client) ClearGraphics(ctx context.Context, pane, layer string) error {
	return c.call(ctx, "pane.graphics.clear", map[string]any{"pane_id": pane, "layer_id": layer}, nil)
}

// PaneOpen describes the plugin pane to open. Width and Height take either
// a cell count or a percentage such as "80%".
type PaneOpen struct {
	Plugin     string
	Entrypoint string
	Placement  string
	Width      string
	Height     string
	Focus      bool
	Env        map[string]string
}

func (c *Client) OpenPane(ctx context.Context, p PaneOpen) (string, error) {
	params := map[string]any{
		"plugin_id":  p.Plugin,
		"entrypoint": p.Entrypoint,
		"focus":      p.Focus,
	}
	if p.Placement != "" {
		params["placement"] = p.Placement
	}
	if len(p.Env) > 0 {
		params["env"] = p.Env
	}
	for key, size := range map[string]string{"width": p.Width, "height": p.Height} {
		if value, ok := popupSize(size); ok {
			params[key] = value
		}
	}
	var result struct {
		PluginPane struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"plugin_pane"`
	}
	err := c.call(ctx, "plugin.pane.open", params, &result)
	return result.PluginPane.Pane.PaneID, err
}

// popupSize keeps a percentage a string and a cell count a number, which is
// the only shape Herdr accepts for each.
func popupSize(size string) (any, bool) {
	if size == "" {
		return nil, false
	}
	if strings.HasSuffix(size, "%") {
		return size, true
	}
	cells, err := strconv.Atoi(size)
	if err != nil {
		return nil, false
	}
	return cells, true
}
