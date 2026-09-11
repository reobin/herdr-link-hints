package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
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

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("herdr rpc error %d: %s", e.Code, e.Message)
}

var requestSeq atomic.Uint64

func nextRequestID() string {
	return fmt.Sprintf("link-hints-%d-%d", os.Getpid(), requestSeq.Add(1))
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return fmt.Errorf("dial herdr socket %s: %w", c.socket, err)
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
