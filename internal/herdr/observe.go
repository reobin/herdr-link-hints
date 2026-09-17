package herdr

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/reobin/herdr-link-hints/internal/ansi"
)

const (
	// Silence this long means no repaint is coming.
	firstFrameWait       = 320 * time.Millisecond
	nudgedFirstFrameWait = 80 * time.Millisecond
	// A repaint arrives as a burst; this much quiet ends it.
	quietAfterFrame = 50 * time.Millisecond
	// Hard stop, so a chatty pane cannot hold the picker open.
	observeLimit = 800 * time.Millisecond
	maxFrameSize = 4 << 20

	nudgeAttempts = 4
	nudgeInterval = 25 * time.Millisecond
)

// ObserveOSC8 sees links whose URL never appears as text.
func (c *Client) ObserveOSC8(ctx context.Context, pane string, cols, rows int) ([]ansi.Link, error) {
	ctx, cancel := context.WithTimeout(ctx, observeLimit)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin, observeArgs(pane, cols, rows)...)
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

	landed := make(chan struct{})
	nudged := c.nudge(ctx, landed)

	return ansi.ParseLinks(collectFrames(ctx, frames, landed, nudged)), nil
}

// nudge wakes the headless loop so it accepts clients now. Repeats stop
// once a frame lands.
func (c *Client) nudge(ctx context.Context, landed <-chan struct{}) <-chan struct{} {
	woke := make(chan struct{}, nudgeAttempts)
	go func() {
		for range nudgeAttempts {
			if err := c.call(ctx, "pane.list", map[string]any{}, nil); err == nil {
				woke <- struct{}{}
			}
			select {
			case <-landed:
				return
			case <-ctx.Done():
				return
			case <-time.After(nudgeInterval):
			}
		}
	}()
	return woke
}

// collectFrames gathers a repaint: wait for start, then quiet.
func collectFrames(ctx context.Context, frames <-chan []byte, landed chan<- struct{}, woke <-chan struct{}) []byte {
	var stream []byte
	first := true
	timer := time.NewTimer(firstFrameWait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return stream
		case <-woke:
			if first {
				resetTimer(timer, nudgedFirstFrameWait)
			}
		case frame, open := <-frames:
			if !open {
				return stream
			}
			if first {
				first = false
				close(landed)
			}
			// Frames are PTY chunks of any size; each holds the window open.
			stream = append(stream, frame...)
			resetTimer(timer, quietAfterFrame)
		case <-timer.C:
			return stream
		}
	}
}

func observeArgs(pane string, cols, rows int) []string {
	args := []string{"terminal", "session", "observe", pane}
	if cols > 0 && rows > 0 {
		args = append(args, "--cols", strconv.Itoa(cols), "--rows", strconv.Itoa(rows))
	}
	return args
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
