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
	// Herdr replays a repaint as soon as the stream opens, so silence this
	// long means there is nothing to see. The unnudged wait has to clear
	// the server's 250ms client-accept poll, and sitting exactly on it
	// throws away frames that were already in flight.
	firstFrameWait = 320 * time.Millisecond
	// Nudged, the accept happens in the same loop iteration, so the wait
	// only has to cover the stream itself.
	nudgedFirstFrameWait = 80 * time.Millisecond
	// A repaint arrives as a burst; this much quiet ends it.
	quietAfterFrame = 50 * time.Millisecond
	// Hard stop, so a chatty pane cannot hold the picker open.
	observeLimit = 800 * time.Millisecond
	maxFrameSize = 4 << 20

	// The nudge can reach the server before the observer has finished
	// connecting, which wins nothing, so it repeats.
	nudgeAttempts = 4
	nudgeInterval = 25 * time.Millisecond
)

// ObserveOSC8 is the only way to see a link whose URL never appears as
// text: snapshots strip OSC 8 targets. The stream is closed before return.
// Unsized, Herdr renders the stream into a frame of its own default shape,
// which wraps the output elsewhere and moves every link.
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

// nudge wakes the headless loop so it runs its client-accept step now. The
// loop otherwise sleeps up to 250ms with nothing in its select watching the
// client listener, which is the entire observe delay. Any API request will
// do; pane.list is absent from the server's UI-dirty set, so it repaints
// nothing. Each success is reported as it happens, because only a nudge
// that got through makes the short first-frame wait safe.
//
// It repeats because a nudge can arrive before the observer has finished
// connecting, which wakes the loop to accept nobody. Repeats stop as soon
// as a frame lands, so the later ones only run when nothing is coming.
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

// collectFrames gathers a repaint: it waits for the stream to start, then
// for it to go quiet. Splitting it out keeps the timing testable without a
// subprocess.
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
			// The accept poll is out of the way, so the wait no longer has
			// to cover it. Unnudged it stands, rather than timing out on a
			// frame that was already in flight.
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
			// A frame is a PTY read chunk of any size, so every one has to
			// hold the window open or a repaint is cut mid-sequence.
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
