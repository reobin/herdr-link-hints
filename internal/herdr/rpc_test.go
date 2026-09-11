package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
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

// fakeServer answers one connection with reply's lines, then closes. reply
// sees the decoded request so it can echo the id.
func fakeServer(t *testing.T, reply func(request map[string]any) [][]byte) string {
	t.Helper()
	socket := socketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var request map[string]any
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			return
		}
		for _, line := range reply(request) {
			if _, err := conn.Write(append(line, '\n')); err != nil {
				return
			}
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
			"error": map[string]any{"code": 42, "message": "no link there"},
		})}
	})

	_, err := New(WithSocket(socket)).ActivateLink(context.Background(), "w1:p1", 0, 0)
	if err == nil {
		t.Fatal("expected the server error to surface")
	}
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) || rpcErr.Code != 42 {
		t.Fatalf("err = %v, want an rpcError with code 42", err)
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
