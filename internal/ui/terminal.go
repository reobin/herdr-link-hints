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
)

const fallbackRows = 24

// Terminal reads single keystrokes in raw mode on a tty, and whole lines
// otherwise, which keeps the picker scriptable and testable.
type Terminal struct {
	out     *bufio.Writer
	keys    <-chan byte
	lines   *bufio.Reader
	restore func()
	rows    int
}

// Open must be paired with Close to put the terminal back.
func Open(in *os.File, out io.Writer) *Terminal {
	t := &Terminal{out: bufio.NewWriter(out), rows: fallbackRows}
	restore, err := enterRaw(in)
	if err != nil {
		t.lines = bufio.NewReader(in)
		return t
	}
	t.restore = restore
	t.keys = readBytes(in)
	if rows, ok := screenRows(in); ok {
		t.rows = rows
	}
	return t
}

func (t *Terminal) interactive() bool { return t.keys != nil }

func (t *Terminal) Close() {
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

// crlf compensates for raw mode, which does not translate newlines.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// Pause waits for any key, so an error stays readable before a popup pane
// closes.
func (t *Terminal) Pause(message string) {
	t.Printf("%s\n", message)
	t.Flush()
	if t.interactive() {
		t.readKey()
		return
	}
	_, _ = t.lines.ReadString('\n')
}

// enterRaw runs stty against the real terminal: Go hands a child
// /dev/null when Stdin is nil, and `stty -g` then fails with "stdin isn't
// a terminal". Twice per session, not twice per keystroke.
func enterRaw(tty *os.File) (func(), error) {
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

func screenRows(tty *os.File) (int, bool) {
	out, err := stty(tty, "size")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, false
	}
	rows, err := strconv.Atoi(fields[0])
	if err != nil || rows <= 0 {
		return 0, false
	}
	return rows, true
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
