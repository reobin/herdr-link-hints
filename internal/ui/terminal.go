// Package ui renders the picker and reads keystrokes.
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
)

const (
	fallbackRows = 24
	fallbackCols = 80
)

const spinnerTick = 90 * time.Millisecond

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Terminal reads keys on a tty, lines otherwise.
type Terminal struct {
	out     *bufio.Writer
	keys    <-chan byte
	lines   *bufio.Reader
	restore func()
	rows    int
	cols    int
}

// Open must pair with Close.
func Open(in *os.File, out io.Writer) *Terminal {
	t := &Terminal{out: bufio.NewWriter(out), rows: fallbackRows, cols: fallbackCols}
	restore, err := enterRaw(in)
	if err != nil {
		t.lines = bufio.NewReader(in)
		return t
	}
	t.restore = restore
	t.keys = readBytes(in)
	// Nothing is typed here, so hide the cursor.
	t.hideCursor()
	if rows, cols, ok := screenSize(in); ok {
		t.rows, t.cols = rows, cols
	}
	return t
}

func (t *Terminal) interactive() bool { return t.keys != nil }

// Size is what the pane got, not what was asked for.
func (t *Terminal) Size() (rows, cols int) { return t.rows, t.cols }

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

// spinnerText drops the label rather than clipping it.
func (t *Terminal) spinnerText(frame int, label string) string {
	spinner := spinnerFrames[frame%len(spinnerFrames)]
	if len([]rune(spinner))+1+len([]rune(label)) > t.cols {
		return spinner
	}
	return spinner + " " + label
}

// Spin animates a label until stop.
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

// crlf compensates for raw mode.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// Pause waits for a key so a message stays readable.
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

// enterRaw sets raw mode, falling back to stty.
func enterRaw(tty *os.File) (func(), error) {
	if restore, err := enterRawIoctl(tty); err == nil {
		return restore, nil
	}
	return enterRawStty(tty)
}

// enterRawStty runs stty on the real terminal.
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

// readBytes streams bytes so reads can have deadlines.
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
