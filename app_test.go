package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/ansi"
	"github.com/reobin/herdr-link-hints/internal/config"
	"github.com/reobin/herdr-link-hints/internal/handoff"
	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/marks"
	"github.com/reobin/herdr-link-hints/internal/theme"
	"github.com/reobin/herdr-link-hints/internal/ui"
)

func TestTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		result   herdr.Activation
		err      error
		fallback string
		wantURL  string
		wantDone bool
	}{
		{
			name:     "resolved but not handled falls back to the browser",
			result:   herdr.Activation{URL: "https://a.io/x"},
			fallback: "https://a.io/x",
			wantURL:  "https://a.io/x",
		},
		{
			name:     "handled means Herdr opened it",
			result:   herdr.Activation{URL: "https://a.io/x", Handled: true},
			fallback: "https://a.io/x",
			wantURL:  "https://a.io/x",
			wantDone: true,
		},
		{
			name:     "a resolved url beats the one we read off screen",
			result:   herdr.Activation{URL: "https://r.io/real"},
			fallback: "https://t.io/typed",
			wantURL:  "https://r.io/real",
		},
		{
			name:     "a failed activation keeps the fallback",
			err:      errors.New("no socket"),
			fallback: "https://t.io/typed",
			wantURL:  "https://t.io/typed",
		},
		{
			name:     "a handled result is ignored when the call errored",
			result:   herdr.Activation{URL: "https://a.io/x", Handled: true},
			err:      errors.New("no socket"),
			fallback: "https://t.io/typed",
			wantURL:  "https://t.io/typed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			url, done := target(tc.result, tc.err, tc.fallback)
			if url != tc.wantURL || done != tc.wantDone {
				t.Fatalf("target() = %q, %v; want %q, %v", url, done, tc.wantURL, tc.wantDone)
			}
		})
	}
}

func TestItemsFor(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{URL: "https://a.io/x", Text: "https://a.io/x", Row: 2, Pane: "w1:p1"},
		{URL: "https://b.io/y", Text: "#232", Row: 5, Pane: "w1:p2"},
	}

	codes := []string{"a", "s"}
	got := itemsFor(found, codes)
	if assigned := []string{got[0].Code, got[1].Code}; !reflect.DeepEqual(assigned, codes) {
		t.Fatalf("itemsFor() codes = %+v", assigned)
	}
}

// Without a layer the pick is a code list.
func TestNarrowOptsFallsBackToListWithoutALayer(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	found := []links.Link{{Pane: "w1:p1", Row: 1, Col: 2, Text: "ab"}}
	codes := []string{"a"}
	ctx := context.Background()

	if opts := narrowOpts(ctx, nil, found, codes); opts.OnNarrow != nil {
		t.Fatal("nil marker should leave OnNarrow unset so the pick continues as a list")
	}
	dead := marks.New(nil, log, theme.Colors{},
		[]herdr.Pane{{ID: "w1:p1", Width: 80, Height: 24}},
		map[string]herdr.Scroll{}, map[string]herdr.Graphics{})
	if opts := narrowOpts(ctx, dead, found, codes); opts.OnNarrow != nil {
		t.Fatal("dead marker should leave OnNarrow unset so the pick continues as a list")
	}
	live := marks.New(nil, log, theme.Colors{},
		[]herdr.Pane{{ID: "w1:p1", Width: 80, Height: 24}},
		map[string]herdr.Scroll{},
		map[string]herdr.Graphics{"w1:p1": {CellWidthPx: 9, CellHeightPx: 19, PaneVisible: true}})
	if !live.Live() {
		t.Fatal("want a live marker for the control case")
	}
	if opts := narrowOpts(ctx, live, found, codes); opts.OnNarrow == nil {
		t.Fatal("live marker should redraw badges on narrow")
	}
}

func TestGrownBy(t *testing.T) {
	t.Parallel()
	if got := clampGrowth(12, 5); got != 7 {
		t.Fatalf("clampGrowth() = %d", got)
	}
	if got := clampGrowth(3, 9); got != 0 {
		t.Fatalf("clampGrowth() = %d, want no negative shift", got)
	}
}

func TestPaneIDs(t *testing.T) {
	t.Parallel()
	got := paneIDs([]herdr.Pane{{ID: "w1:p1", Width: 206, Height: 59}, {ID: "w1:p2"}})
	if want := []string{"w1:p1", "w1:p2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paneIDs() = %+v, want %+v", got, want)
	}
}

// fakeClient answers the Herdr calls both flows make, recording the writes.
type fakeClient struct {
	mu       sync.Mutex
	lines    map[string][]string
	panes    []herdr.Pane
	scrolls  map[string]herdr.Scroll
	infos    map[string]herdr.Graphics
	opened   []herdr.PaneOpen
	openErr  error
	set      []string
	cleared  []string
	activate herdr.Activation
}

func (f *fakeClient) PaneLines(_ context.Context, pane string) ([]string, error) {
	return f.lines[pane], nil
}

func (f *fakeClient) ObserveOSC8(context.Context, string, int, int) ([]ansi.Link, error) {
	return nil, nil
}

func (f *fakeClient) ScreenPanes(context.Context, string) ([]herdr.Pane, error) {
	return f.panes, nil
}

func (f *fakeClient) PaneScrolls(context.Context) (map[string]herdr.Scroll, error) {
	return f.scrolls, nil
}

func (f *fakeClient) PaneScroll(_ context.Context, pane string) (herdr.Scroll, error) {
	return f.scrolls[pane], nil
}

func (f *fakeClient) GraphicsInfos(context.Context, []string) map[string]herdr.Graphics {
	return f.infos
}

func (f *fakeClient) ActivateLink(context.Context, string, int, int) (herdr.Activation, error) {
	return f.activate, nil
}

func (f *fakeClient) OpenPane(_ context.Context, p herdr.PaneOpen) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, p)
	if f.openErr != nil {
		return "", f.openErr
	}
	return "w1:p9", nil
}

func (f *fakeClient) SetGraphics(_ context.Context, frame herdr.Frame) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.set = append(f.set, frame.Layer)
	return nil
}

func (f *fakeClient) ClearGraphics(_ context.Context, _, layer string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, layer)
	return nil
}

func (f *fakeClient) seen() (set, cleared []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.set), slices.Clone(f.cleared)
}

func newFakeClient(lines ...string) *fakeClient {
	return &fakeClient{
		lines:   map[string][]string{"w1:p1": lines},
		panes:   []herdr.Pane{{ID: "w1:p1", Width: 80, Height: 24}},
		scrolls: map[string]herdr.Scroll{"w1:p1": {ViewportRows: 24}},
		infos: map[string]herdr.Graphics{
			"w1:p1": {CellWidthPx: 9, CellHeightPx: 19, PaneVisible: true, MaxLayers: 16},
		},
	}
}

func testApp(t *testing.T, f *fakeClient, cfg config.Config) *app {
	t.Helper()
	return &app{cfg: cfg, log: slog.New(slog.DiscardHandler), client: f}
}

func TestOpenHandsTheScanToThePickerPane(t *testing.T) {
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
	f := newFakeClient("see https://a.io/x for more")
	cfg := config.Load()
	cfg.StateDir = t.TempDir()
	a := testApp(t, f, cfg)

	if code := a.open(context.Background()); code != exitOK {
		t.Fatalf("open() = %d, want %d", code, exitOK)
	}
	if len(f.opened) != 1 {
		t.Fatalf("open() asked for %d panes, want 1", len(f.opened))
	}
	path := f.opened[0].Env[handoff.EnvVar]
	if path == "" {
		t.Fatal("open() left no handoff on the picker's environment")
	}
	p, ok := handoff.Read(path, slog.New(slog.DiscardHandler))
	if !ok {
		t.Fatal("the handoff open() wrote was rejected on read")
	}
	if len(p.Found) != 1 || p.Found[0].URL != "https://a.io/x" {
		t.Fatalf("handoff carried %+v, want the one link on screen", p.Found)
	}
}

func TestOpenClearsWhatItDrewWhenThePaneWillNotOpen(t *testing.T) {
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
	themeEnv(t)
	if err := theme.Save("test-open", theme.Fallback()); err != nil {
		t.Fatal(err)
	}
	f := newFakeClient("see https://a.io/x for more")
	f.openErr = errors.New("no room")
	cfg := config.Load()
	cfg.StateDir, cfg.TermProgram = t.TempDir(), "test-open"
	a := testApp(t, f, cfg)

	if code := a.open(context.Background()); code != exitFailed {
		t.Fatalf("open() = %d, want %d", code, exitFailed)
	}
	set, cleared := f.seen()
	if len(set) == 0 {
		t.Fatal("a warm theme cache should have let open() draw before asking for the pane")
	}
	if len(cleared) == 0 {
		t.Fatal("open() left its overlay up after the pane failed to open")
	}
}

// Fed a handoff, the picker must not scan again.
func TestPickOpensTheCodeItIsGiven(t *testing.T) {
	f := newFakeClient("see https://a.io/x for more")
	f.activate = herdr.Activation{URL: "https://a.io/x", Handled: true}
	dir := t.TempDir()
	a := testApp(t, f, config.Config{StateDir: dir})

	p := a.gather(context.Background(), "w1:p1")
	p.Colors, p.HasColors = theme.Fallback(), true
	path, err := handoff.Write(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.HandoffPath = path

	term, out := pipeTerminal(t, "a\n")
	if code := a.pick(context.Background(), term, theme.Fallback); code != exitOK {
		t.Fatalf("pick() = %d, want %d", code, exitOK)
	}
	term.Close()
	if got := out.String(); !strings.Contains(got, "https://a.io/x") {
		t.Fatalf("pick() reported %q, want the opened url", got)
	}
}

// Nothing on screen is a quit, not a failure.
func TestPickStopsWhenThereIsNothingToHint(t *testing.T) {
	f := newFakeClient("no links here")
	dir := t.TempDir()
	a := testApp(t, f, config.Config{StateDir: dir})

	p := a.gather(context.Background(), "w1:p1")
	p.Colors, p.HasColors = theme.Fallback(), true
	path, err := handoff.Write(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.HandoffPath = path

	term, out := pipeTerminal(t, "\n")
	if code := a.pick(context.Background(), term, theme.Fallback); code != exitCancelled {
		t.Fatalf("pick() = %d, want %d", code, exitCancelled)
	}
	term.Close()
	if got := out.String(); !strings.Contains(got, "no links") {
		t.Fatalf("pick() said %q, want it to say there were no links", got)
	}
}

func TestPickScansItselfWithoutAHandoff(t *testing.T) {
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
	f := newFakeClient("see https://a.io/x for more")
	f.activate = herdr.Activation{URL: "https://a.io/x", Handled: true}
	cfg := config.Load()
	cfg.StateDir = t.TempDir()
	a := testApp(t, f, cfg)

	term, out := pipeTerminal(t, "a\n")
	if code := a.pick(context.Background(), term, theme.Fallback); code != exitOK {
		t.Fatalf("pick() = %d, want %d", code, exitOK)
	}
	term.Close()
	if got := out.String(); !strings.Contains(got, "https://a.io/x") {
		t.Fatalf("pick() reported %q, want the opened url", got)
	}
}

// pipeTerminal drives the picker down its non-tty path, a code per line.
func pipeTerminal(t *testing.T, typed string) (*ui.Terminal, *bytes.Buffer) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	go func() {
		defer func() { _ = writer.Close() }()
		_, _ = io.WriteString(writer, typed)
	}()
	var out bytes.Buffer
	return ui.Open(reader, &out), &out
}

// themeEnv gives a test its own cache directory.
func themeEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
}
