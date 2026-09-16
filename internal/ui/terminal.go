// Package ui renders the hint picker and reads the user's keystrokes.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/reobin/herdr-link-hints/internal/theme"
)

const (
	fallbackRows = 24
	fallbackCols = 80
)

// Every colour query goes out before anything is read, so one deadline
// covers the lot. maxReply fits rgb: components of any width.
const (
	themeWait = 150 * time.Millisecond
	maxReply  = 64
)

const spinnerTick = 90 * time.Millisecond

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Terminal reads single keystrokes in raw mode on a tty, and whole lines
// otherwise, which keeps the picker scriptable and testable.
type Terminal struct {
	out     *bufio.Writer
	keys    <-chan byte
	lines   *bufio.Reader
	restore func()
	rows    int
	cols    int
}

// Open must be paired with Close to put the terminal back.
func Open(in *os.File, out io.Writer) *Terminal {
	t := &Terminal{out: bufio.NewWriter(out), rows: fallbackRows, cols: fallbackCols}
	restore, err := enterRaw(in)
	if err != nil {
		t.lines = bufio.NewReader(in)
		return t
	}
	t.restore = restore
	t.keys = readBytes(in)
	// Nothing here is typed into, so a blinking cursor only invites it.
	t.hideCursor()
	if rows, cols, ok := screenSize(in); ok {
		t.rows, t.cols = rows, cols
	}
	return t
}

func (t *Terminal) interactive() bool { return t.keys != nil }

// Size is what the pane actually got, not what was asked for.
func (t *Terminal) Size() (rows, cols int) { return t.rows, t.cols }

// Theme asks the terminal what colours it is painted in, falling back to
// black and white when it does not answer. A full reply is remembered on
// disk keyed by TERM_PROGRAM for a day, so the next run under the same
// terminal skips the round trip and its wait. Delete
// ~/Library/Caches/herdr-link-hints/theme-*.json or set
// HINTS_NO_THEME_CACHE=1 to force a live probe after a light/dark switch.
func (t *Terminal) Theme() theme.Colors {
	colors := theme.Fallback()
	if !t.interactive() {
		return colors
	}
	program := os.Getenv("TERM_PROGRAM")
	if cached, ok := theme.Load(program); ok {
		return cached
	}
	for _, key := range theme.Keys {
		_, _ = fmt.Fprint(t.out, theme.Query(key))
	}
	t.Flush()
	seen := make(map[string]bool, len(theme.Keys))
	deadline := time.Now().Add(themeWait)
	for range theme.Keys {
		reply, ok := t.readReply(deadline)
		if !ok {
			break
		}
		if key, rgb, ok := theme.Parse(reply); ok {
			switch key {
			case theme.KeyForeground, theme.KeyBackground, theme.KeyAccentRed, theme.KeyAccent, theme.KeyAccentBlue:
				colors.Set(key, rgb)
				seen[key] = true
			}
		}
	}
	if len(seen) == len(theme.Keys) {
		_ = theme.Save(program, colors)
	}
	return colors
}

// readReply collects one OSC report, which ends at either BEL or ST.
func (t *Terminal) readReply(deadline time.Time) (string, bool) {
	var reply strings.Builder
	for reply.Len() < maxReply {
		wait := time.Until(deadline)
		if wait <= 0 {
			return "", false
		}
		b, ok := t.nextByte(wait)
		if !ok {
			return "", false
		}
		reply.WriteByte(b)
		if b == '\a' || strings.HasSuffix(reply.String(), "\x1b\\") {
			return reply.String(), true
		}
	}
	return "", false
}

func (t *Terminal) Close() {
	if t.restore != nil {
		t.showCursor()
	}
	_ = t.out.Flush()
	if t.restore != nil {
		t.restore()
		t.restore = nil
	}
}

func (t *Terminal) Printf(format string, args ...any) {
	_, _ = fmt.Fprint(t.out, crlf(fmt.Sprintf(format, args...)))
}

func (t *Terminal) Clear() {
	_, _ = fmt.Fprint(t.out, "\x1b[2J\x1b[H")
}

func (t *Terminal) Flush() { _ = t.out.Flush() }

// spinnerText drops the label rather than clipping it: the annotate pane is
// a few cells wide, and half a word reads worse than none.
func (t *Terminal) spinnerText(frame int, label string) string {
	spinner := spinnerFrames[frame%len(spinnerFrames)]
	if len([]rune(spinner))+1+len([]rune(label)) > t.cols {
		return spinner
	}
	return spinner + " " + label
}

// Spin animates a label until stop is called, and is the only writer to
// the terminal in the meantime.
func (t *Terminal) Spin(label string) (stop func()) {
	if !t.interactive() {
		t.Printf("%s\n", label)
		t.Flush()
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(spinnerTick)
		defer ticker.Stop()
		for frame := 0; ; frame++ {
			t.centred(line{{text: t.spinnerText(frame, label), sgr: "2"}})
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func (t *Terminal) hideCursor() { _, _ = fmt.Fprint(t.out, "\x1b[?25l") }

func (t *Terminal) showCursor() { _, _ = fmt.Fprint(t.out, "\x1b[?25h") }

// crlf compensates for raw mode, which does not translate newlines.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// Pause waits for any key, so an error stays readable before a popup pane
// closes. It takes the pane over rather than printing over the spinner.
func (t *Terminal) Pause(message string) {
	if !t.interactive() {
		t.Printf("%s\n", message)
		t.Flush()
		_, _ = t.lines.ReadString('\n')
		return
	}
	t.centred(line{{text: message, sgr: "2"}})
	t.readKey()
}

// enterRaw sets raw mode through termios, and keeps the stty path for any
// terminal the ioctl will not answer for.
func enterRaw(tty *os.File) (func(), error) {
	if restore, err := enterRawIoctl(tty); err == nil {
		return restore, nil
	}
	return enterRawStty(tty)
}

// enterRawStty runs stty against the real terminal: Go hands a child
// /dev/null when Stdin is nil, and `stty -g` then fails.
func enterRawStty(tty *os.File) (func(), error) {
	saved, err := stty(tty, "-g")
	if err != nil {
		return nil, err
	}
	if _, err := stty(tty, "raw", "-echo"); err != nil {
		return nil, err
	}
	return func() { _, _ = stty(tty, strings.TrimSpace(saved)) }, nil
}

func stty(tty *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	out, err := cmd.Output()
	return string(out), err
}

func screenSize(tty *os.File) (rows, cols int, ok bool) {
	if rows, cols, ok := screenSizeIoctl(tty); ok {
		return rows, cols, true
	}
	return screenSizeStty(tty)
}

func screenSizeStty(tty *os.File) (rows, cols int, ok bool) {
	out, err := stty(tty, "size")
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, false
	}
	rows, rowErr := strconv.Atoi(fields[0])
	cols, colErr := strconv.Atoi(fields[1])
	if rowErr != nil || colErr != nil || rows <= 0 || cols <= 0 {
		return 0, 0, false
	}
	return rows, cols, true
}

// readBytes streams one byte at a time, so a read can be given a deadline
// without putting the descriptor in non-blocking mode.
func readBytes(in *os.File) <-chan byte {
	out := make(chan byte)
	go func() {
		defer close(out)
		buf := make([]byte, 1)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				out <- buf[0]
			}
			if err != nil {
				return
			}
		}
	}()
	return out
}

func (t *Terminal) nextByte(wait time.Duration) (byte, bool) {
	if wait <= 0 {
		b, open := <-t.keys
		return b, open
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case b, open := <-t.keys:
		return b, open
	case <-timer.C:
		return 0, false
	}
}
