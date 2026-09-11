package herdr

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/reobin/herdr-link-hints/internal/ansi"
)

const (
	// Herdr replays a repaint as soon as the stream opens, so silence this
	// long means there is nothing to see.
	firstFrameWait = 250 * time.Millisecond
	// A repaint arrives as a burst; this much quiet ends it.
	quietAfterFrame = 50 * time.Millisecond
	// Hard stop, so a chatty pane cannot hold the picker open.
	observeLimit = 800 * time.Millisecond
	maxFrameSize = 4 << 20
)

// ObserveOSC8 is the only way to see a link whose URL never appears as
// text, since snapshots strip OSC 8 targets. The stream is closed before
// returning: nothing runs between keypresses.
func (c *Client) ObserveOSC8(ctx context.Context, pane string) ([]ansi.Link, error) {
	ctx, cancel := context.WithTimeout(ctx, observeLimit)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin, "terminal", "session", "observe", pane)
	cmd.Stdin = nil
	cmd.Stderr = nil
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("observe %s: %w", pane, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("observe %s: %w", pane, err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	frames := make(chan []byte)
	go readFrames(ctx, stdout, frames)

	var stream []byte
	timer := time.NewTimer(firstFrameWait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ansi.ParseLinks(stream), nil
		case frame, open := <-frames:
			if !open {
				return ansi.ParseLinks(stream), nil
			}
			// A frame is a PTY read chunk of any size, so every one has to
			// hold the window open or a repaint is cut mid-sequence.
			stream = append(stream, frame...)
			resetTimer(timer, quietAfterFrame)
		case <-timer.C:
			return ansi.ParseLinks(stream), nil
		}
	}
}

func readFrames(ctx context.Context, r io.Reader, out chan<- []byte) {
	defer close(out)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxFrameSize)
	for scanner.Scan() {
		var frame struct {
			Bytes string `json:"bytes"`
		}
		if json.Unmarshal(scanner.Bytes(), &frame) != nil || frame.Bytes == "" {
			continue
		}
		raw, err := decodeFrame(frame.Bytes)
		if err != nil {
			continue
		}
		select {
		case out <- raw:
		case <-ctx.Done():
			// No reader outlives the request.
			return
		}
	}
}

func decodeFrame(encoded string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return raw, nil
	}
	return base64.RawStdEncoding.DecodeString(encoded)
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
