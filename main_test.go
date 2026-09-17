package main

import (
	"bytes"
	"log/slog"
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
