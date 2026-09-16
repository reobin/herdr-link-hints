package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// socketPath returns a path short enough to bind: a unix socket address
// is capped near 104 bytes, which t.TempDir() alone can exceed.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hl")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// fakeServer answers every request on every connection with reply's
// lines. reply sees the decoded request so it can echo the id.
func fakeServer(t *testing.T, reply func(request map[string]any) [][]byte) string {
	return serve(t, nil, reply)
}

func serve(t *testing.T, conns *atomic.Int64, reply func(request map[string]any) [][]byte) string {
	t.Helper()
	socket := socketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if conns != nil {
				conns.Add(1)
			}
			go func() {
				defer func() { _ = conn.Close() }()
				decoder := json.NewDecoder(conn)
				for {
					var request map[string]any
					if err := decoder.Decode(&request); err != nil {
						return
					}
					for _, line := range reply(request) {
						if _, err := conn.Write(append(line, '\n')); err != nil {
							return
						}
					}
				}
			}()
		}
	}()
	return socket
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestActivateLink(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id":     request["id"],
			"result": map[string]any{"url": "https://a.io/x", "handled": true},
		})}
	})

	result, err := New(WithSocket(socket)).ActivateLink(context.Background(), "w1:p1", 3, 10)
	if err != nil {
		t.Fatalf("ActivateLink: %v", err)
	}
	if result.URL != "https://a.io/x" || !result.Handled {
		t.Fatalf("ActivateLink() = %+v", result)
	}

	request := <-requests
	if request["method"] != "pane.link.activate" {
		t.Fatalf("method = %v", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["pane_id"] != "w1:p1" || params["viewport_row"] != float64(3) || params["col"] != float64(10) {
		t.Fatalf("params = %+v", params)
	}
}

func TestActivateLinkSkipsUnrelatedMessages(t *testing.T) {
	t.Parallel()
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		return [][]byte{
			mustJSON(t, map[string]any{"method": "pane.output", "params": map[string]any{}}),
			mustJSON(t, map[string]any{"id": "someone-else", "result": map[string]any{"url": "https://wrong.io"}}),
			mustJSON(t, map[string]any{
				"id":     request["id"],
				"result": map[string]any{"url": "https://right.io", "handled": false},
			}),
		}
	})

	result, err := New(WithSocket(socket)).ActivateLink(context.Background(), "w1:p1", 0, 0)
	if err != nil {
		t.Fatalf("ActivateLink: %v", err)
	}
	if result.URL != "https://right.io" || result.Handled {
		t.Fatalf("ActivateLink() = %+v", result)
	}
}

func TestActivateLinkReportsServerError(t *testing.T) {
	t.Parallel()
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		return [][]byte{mustJSON(t, map[string]any{
			"id":    request["id"],
			"error": map[string]any{"code": "invalid_params", "message": "no link there"},
		})}
	})

	_, err := New(WithSocket(socket)).ActivateLink(context.Background(), "w1:p1", 0, 0)
	if err == nil {
		t.Fatal("expected the server error to surface")
	}
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) || rpcErr.Code != "invalid_params" {
		t.Fatalf("err = %v, want an rpcError with code invalid_params", err)
	}
	if !strings.Contains(err.Error(), "no link there") {
		t.Fatalf("err = %v, want the server's message kept", err)
	}
}

func TestActivateLinkTimesOutOnSilence(t *testing.T) {
	t.Parallel()
	socket := fakeServer(t, func(map[string]any) [][]byte { return nil })

	client := New(WithSocket(socket), WithTimeouts(time.Second, 150*time.Millisecond))
	start := time.Now()
	if _, err := client.ActivateLink(context.Background(), "w1:p1", 0, 0); err == nil {
		t.Fatal("expected a timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %s, want the request to give up promptly", elapsed)
	}
}

func TestActivateLinkWithoutServer(t *testing.T) {
	t.Parallel()
	if _, err := New(WithSocket(socketPath(t))).ActivateLink(context.Background(), "w1:p1", 0, 0); err == nil {
		t.Fatal("expected a dial error")
	}
}

// The real server answers one request per connection and closes, so a
// client dials once per call rather than holding a connection open.
func TestClientDialsPerCall(t *testing.T) {
	t.Parallel()
	var conns atomic.Int64
	socket := serve(t, &conns, func(request map[string]any) [][]byte {
		return [][]byte{mustJSON(t, map[string]any{
			"id":     request["id"],
			"result": map[string]any{"url": "https://a.io/x", "handled": true},
		})}
	})

	client := New(WithSocket(socket))
	for range 3 {
		if _, err := client.ActivateLink(context.Background(), "w1:p1", 0, 0); err != nil {
			t.Fatalf("ActivateLink: %v", err)
		}
	}
	if got := conns.Load(); got != 3 {
		t.Fatalf("dialed %d connections, want 3", got)
	}
}

// A failed call closes its connection, so the next call dials fresh
// instead of reading from a dead socket.
func TestFailedCallRedials(t *testing.T) {
	t.Parallel()
	var conns atomic.Int64
	socket := serve(t, &conns, func(request map[string]any) [][]byte { return nil })

	client := New(WithSocket(socket), WithTimeouts(time.Second, 100*time.Millisecond))
	if _, err := client.ActivateLink(context.Background(), "w1:p1", 0, 0); err == nil {
		t.Fatal("expected a timeout")
	}
	if _, err := client.ActivateLink(context.Background(), "w1:p1", 0, 0); err == nil {
		t.Fatal("expected a timeout")
	}
	if got := conns.Load(); got != 2 {
		t.Fatalf("dialed %d connections, want 2", got)
	}
}

func TestGraphicsInfos(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 2)
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		requests <- request
		return [][]byte{mustJSON(t, map[string]any{
			"id":     request["id"],
			"result": map[string]any{"cell_width_px": 9, "cell_height_px": 20, "pane_visible": true},
		})}
	})

	infos := New(WithSocket(socket)).GraphicsInfos(context.Background(), []string{"w1:p1", "w1:p2"})
	if len(infos) != 2 {
		t.Fatalf("GraphicsInfos() = %+v, want both panes", infos)
	}
	for i := range 2 {
		request := <-requests
		if request["method"] != "pane.graphics.info" {
			t.Fatalf("request %d method = %v", i, request["method"])
		}
	}
}

func TestGraphicsInfosSkipsFailures(t *testing.T) {
	t.Parallel()
	socket := fakeServer(t, func(request map[string]any) [][]byte {
		params, _ := request["params"].(map[string]any)
		if params["pane_id"] == "w1:p2" {
			return [][]byte{mustJSON(t, map[string]any{
				"id":    request["id"],
				"error": map[string]any{"code": "not_found", "message": "gone"},
			})}
		}
		return [][]byte{mustJSON(t, map[string]any{
			"id":     request["id"],
			"result": map[string]any{"cell_width_px": 9, "cell_height_px": 20, "pane_visible": true},
		})}
	})

	infos := New(WithSocket(socket)).GraphicsInfos(context.Background(), []string{"w1:p1", "w1:p2"})
	if len(infos) != 1 || infos["w1:p1"].CellWidthPx != 9 {
		t.Fatalf("GraphicsInfos() = %+v, want only w1:p1", infos)
	}
}

// The request is encoded before the socket is dialled: the server polls for
// a request line every 100ms, so anything done between connect and write
// waits out a poll. An encode that cannot succeed must therefore never
// reach the dial.
func TestCallEncodesBeforeDialling(t *testing.T) {
	t.Parallel()
	var conns atomic.Int64
	socket := serve(t, &conns, func(request map[string]any) [][]byte {
		return [][]byte{mustJSON(t, map[string]any{"id": request["id"]})}
	})

	client := New(WithSocket(socket))
	err := client.call(context.Background(), "pane.list", map[string]any{"bad": make(chan int)}, nil)
	if err == nil {
		t.Fatal("expected an encode error")
	}
	if got := conns.Load(); got != 0 {
		t.Fatalf("dialed %d connections, want 0: the encode ran after the dial", got)
	}
}
