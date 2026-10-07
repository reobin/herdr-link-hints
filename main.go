// Command picker shows keyboard link hints for the panes on screen.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/reobin/herdr-link-hints/internal/config"
	"github.com/reobin/herdr-link-hints/internal/demo"
	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/ui"
)

// exitCancelled is user quit, not failure.
const (
	exitOK        = 0
	exitFailed    = 1
	exitCancelled = 3
)

const (
	pluginID   = "herdr-link-hints"
	entrypoint = "picker"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	mode := ""
	if len(args) > 0 {
		mode = args[0]
	}
	switch mode {
	case "--demo":
		term := ui.Open(os.Stdin, os.Stdout)
		defer term.Close()
		return runDemo(term)
	case "--open":
		ctx, stop, a := start()
		defer stop()
		return a.open(ctx)
	case "":
		ctx, stop, a := start()
		defer stop()
		term := ui.Open(os.Stdin, os.Stdout)
		defer term.Close()
		return a.pick(ctx, term)
	default:
		_, _ = io.WriteString(os.Stderr, "picker: unknown argument "+mode+"\nusage: picker [--open|--demo]\n")
		return exitFailed
	}
}

// start reads the environment once and wires the flows to a real Herdr.
func start() (context.Context, context.CancelFunc, *app) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	cfg := config.Load()
	log := newLogger(cfg.Debug)
	return ctx, stop, &app{
		cfg:    cfg,
		log:    log,
		client: herdr.New(herdr.WithLogger(log)),
	}
}

func runDemo(term *ui.Terminal) int {
	found := demo.Ranked()

	index, picked := ui.Pick(term, itemsFor(found))
	if !picked {
		return exitCancelled
	}
	term.Printf("\nOpened %s in demo.\n", found[index].URL)
	return exitOK
}

// newLogger logs to stderr, or to the file target names.
func newLogger(target string) *slog.Logger {
	if target == "" {
		return slog.New(slog.DiscardHandler)
	}
	out := io.Writer(os.Stderr)
	if strings.ContainsRune(target, os.PathSeparator) {
		file, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			out = file
		}
	}
	handler := slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(&durationHandler{Handler: handler, start: time.Now()})
}

// durationHandler stamps records with uptime.
type durationHandler struct {
	slog.Handler
	start time.Time
}

func (h *durationHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.Int64("duration_ms", time.Since(h.start).Milliseconds()))
	return h.Handler.Handle(ctx, r)
}

func (h *durationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &durationHandler{Handler: h.Handler.WithAttrs(attrs), start: h.start}
}

func (h *durationHandler) WithGroup(name string) slog.Handler {
	return &durationHandler{Handler: h.Handler.WithGroup(name), start: h.start}
}
