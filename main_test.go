package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Duration stamps startup cost.
func TestLoggerStampsDuration(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(&durationHandler{
		Handler: slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		start:   time.Now().Add(-time.Second),
	})
	log.Debug("picker pane", "rows", 5)
	out := buf.String()
	if !strings.Contains(out, "duration_ms=") {
		t.Fatalf("log line has no duration: %q", out)
	}
}

// Wrapping must not drop the stamp.
func TestLoggerStampsThroughWrappers(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(&durationHandler{
		Handler: slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		start:   time.Now().Add(-time.Second),
	})
	log.With("pane", "w1:p1").Debug("attrs")
	log.WithGroup("scan").Debug("group")
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !strings.Contains(line, "duration_ms=") {
			t.Fatalf("log line has no duration: %q", line)
		}
	}
}

func TestNewLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hints.log")
	tests := []struct {
		name    string
		target  string
		enabled bool
		wants   string
	}{
		{name: "no target discards"},
		{name: "a bare value logs to stderr", target: "1", enabled: true},
		{name: "a path logs to the file", target: path, enabled: true, wants: path},
		{name: "an unopenable path falls back to stderr", target: filepath.Join(dir, "gone", "hints.log"), enabled: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log := newLogger(tc.target)
			log.Debug("picker pane", "rows", 5)
			if got := log.Enabled(context.Background(), slog.LevelDebug); got != tc.enabled {
				t.Fatalf("newLogger(%q) enabled = %v, want %v", tc.target, got, tc.enabled)
			}
			if tc.wants == "" {
				return
			}
			body, err := os.ReadFile(tc.wants)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "duration_ms=") {
				t.Fatalf("%s holds %q, want a stamped record", tc.wants, body)
			}
		})
	}
}

// start wires a real Herdr without touching it.
func TestStartWiresTheApp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	t.Setenv("HINTS_DEBUG", "")

	ctx, stop, a := start()
	defer stop()

	if ctx.Err() != nil {
		t.Fatalf("start() context = %v, want live", ctx.Err())
	}
	if a.cfg.StateDir != dir {
		t.Fatalf("start() StateDir = %q, want %q", a.cfg.StateDir, dir)
	}
	if a.client == nil || a.trail == nil || a.log == nil {
		t.Fatalf("start() = %+v, want every field wired", a)
	}
}

func TestRunRejectsAnUnknownArgument(t *testing.T) {
	t.Parallel()
	if code := run([]string{"--bogus"}); code != exitFailed {
		t.Fatalf("run() = %d, want %d", code, exitFailed)
	}
}

func TestRunDemoOpensTheLinkItIsGiven(t *testing.T) {
	term, out := pipeTerminal(t, "a\n")
	if code := runDemo(term); code != exitOK {
		t.Fatalf("runDemo() = %d, want %d", code, exitOK)
	}
	term.Close()
	if got := out.String(); !strings.Contains(got, "in demo") {
		t.Fatalf("runDemo() said %q, want it to report the demo open", got)
	}
}

// Esc or an unmatched code is a quit, not a failure.
func TestRunDemoCancels(t *testing.T) {
	term, _ := pipeTerminal(t, "zz\n")
	if code := runDemo(term); code != exitCancelled {
		t.Fatalf("runDemo() = %d, want %d", code, exitCancelled)
	}
	term.Close()
}
