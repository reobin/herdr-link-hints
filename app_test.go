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
	mu sync.Mutex
	// rescan, when set, answers every read after the first, so a test can
	// scroll the pane out from under a scan.
	lines       map[string][]string
	rescan      map[string][]string
	reads       int
	area        herdr.Rect
	panes       []herdr.Pane
	panesErr    error
	scrolls     map[string]herdr.Scroll
	scrollsErr  error
	scrollErr   error
	infos       map[string]herdr.Graphics
	opened      []herdr.PaneOpen
	openErr     error
	set         []string
	cleared     []string
	activate    herdr.Activation
	activateErr error
}

func (f *fakeClient) PaneLines(_ context.Context, pane string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.rescan != nil && f.reads > 1 {
		return f.rescan[pane], nil
	}
	return f.lines[pane], nil
}

func (f *fakeClient) ObserveOSC8(context.Context, string, int, int) ([]ansi.Link, error) {
	return nil, nil
}

func (f *fakeClient) ScreenPanes(context.Context, string) (herdr.Layout, error) {
	if f.panesErr != nil {
		return herdr.Layout{}, f.panesErr
	}
	return herdr.Layout{Area: f.area, Panes: f.panes}, nil
}

func (f *fakeClient) PaneScrolls(context.Context) (map[string]herdr.Scroll, error) {
	if f.scrollsErr != nil {
		return nil, f.scrollsErr
	}
	return f.scrolls, nil
}

func (f *fakeClient) PaneScroll(_ context.Context, pane string) (herdr.Scroll, error) {
	if f.scrollErr != nil {
		return herdr.Scroll{}, f.scrollErr
	}
	return f.scrolls[pane], nil
}

func (f *fakeClient) GraphicsInfos(context.Context, []string) map[string]herdr.Graphics {
	return f.infos
}

func (f *fakeClient) ActivateLink(context.Context, string, int, int) (herdr.Activation, error) {
	return f.activate, f.activateErr
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
		area:    herdr.Rect{Width: 80, Height: 24},
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

// A cold theme cache is the CI case: no drawing, just the handoff.
func TestOpenHandsTheScanToThePickerPane(t *testing.T) {
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
	themeEnv(t)
	f := newFakeClient("see https://a.io/x for more")
	cfg := config.Load()
	cfg.StateDir, cfg.TermProgram = t.TempDir(), "test-cold"
	a := testApp(t, f, cfg)

	if code := a.open(context.Background()); code != exitOK {
		t.Fatalf("open() = %d, want %d", code, exitOK)
	}
	if set, _ := f.seen(); len(set) != 0 {
		t.Fatal("a cold theme cache should have left the drawing to the picker pane")
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

// noPaneEnv silences every source config reads a focused pane from.
func noPaneEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HERDR_ACTIVE_PANE_ID", "")
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "")
}

// themeEnv gives a test its own cache directory.
func themeEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
}

func TestPaneSpec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cfg       config.Config
		wantDebug string
	}{
		{
			name: "a popup carries the fixed size",
			cfg:  config.Config{},
		},
		{
			name:      "debug crosses into the pane process",
			cfg:       config.Config{Debug: "debug.log"},
			wantDebug: "debug.log",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := (&app{cfg: tc.cfg}).paneSpec()
			if spec.Plugin != pluginID || spec.Entrypoint != entrypoint {
				t.Fatalf("paneSpec() = %q/%q, want %q/%q", spec.Plugin, spec.Entrypoint, pluginID, entrypoint)
			}
			if spec.Placement != "popup" {
				t.Fatalf("paneSpec() placement = %q, want %q", spec.Placement, "popup")
			}
			if spec.Width != config.PopupWidth || spec.Height != config.PopupHeight {
				t.Fatalf("paneSpec() size = %sx%s, want %sx%s", spec.Width, spec.Height, config.PopupWidth, config.PopupHeight)
			}
			if got := spec.Env["HINTS_DEBUG"]; got != tc.wantDebug {
				t.Fatalf("paneSpec() HINTS_DEBUG = %q, want %q", got, tc.wantDebug)
			}
		})
	}
}

// pane.list is allowed to miss a pane; pane.get fills the gap.
func TestScrollsForReadsThePanesPaneListMissed(t *testing.T) {
	t.Parallel()
	f := newFakeClient()
	f.scrolls = map[string]herdr.Scroll{
		"w1:p1": {ViewportRows: 24, Offset: 3},
		"w1:p2": {ViewportRows: 24, Offset: 7},
	}
	a := testApp(t, f, config.Config{})

	listed := map[string]herdr.Scroll{"w1:p1": {ViewportRows: 24, Offset: 3}}
	got := a.scrollsFor(context.Background(), []string{"w1:p1", "w1:p2"}, listed)

	want := map[string]herdr.Scroll{
		"w1:p1": {ViewportRows: 24, Offset: 3},
		"w1:p2": {ViewportRows: 24, Offset: 7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scrollsFor() = %+v, want %+v", got, want)
	}
}

func TestScrollsForKeepsAPaneAFailedReadCouldNotAnswer(t *testing.T) {
	t.Parallel()
	f := newFakeClient()
	f.scrollErr = errors.New("no socket")
	a := testApp(t, f, config.Config{})

	got := a.scrollsFor(context.Background(), []string{"w1:p1", "w1:p2"}, nil)

	want := map[string]herdr.Scroll{"w1:p1": {}, "w1:p2": {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scrollsFor() = %+v, want a zero scroll per pane", got)
	}
}

func TestGrownByTreatsAFailedReadAsNoGrowth(t *testing.T) {
	t.Parallel()
	f := newFakeClient()
	f.scrollErr = errors.New("no socket")
	a := testApp(t, f, config.Config{})

	before := map[string]herdr.Scroll{"w1:p1": {Offset: 4}}
	if got := a.grownBy(context.Background(), "w1:p1", before); got != 0 {
		t.Fatalf("grownBy() = %d, want 0", got)
	}
}

func TestActivateFallsBackWhenTheCallFails(t *testing.T) {
	t.Parallel()
	f := newFakeClient()
	f.activate = herdr.Activation{URL: "https://resolved.io/x", Handled: true}
	f.activateErr = errors.New("no socket")
	a := testApp(t, f, config.Config{})

	url, handled := a.activate(context.Background(), links.Link{Pane: "w1:p1", URL: "https://typed.io/x"}, 1, 1)
	if url != "https://typed.io/x" || handled {
		t.Fatalf("activate() = %q/%v, want the url we read off screen and no handoff", url, handled)
	}
}

// A layout or list failure degrades to a one-pane scan, not a crash.
func TestGatherSurvivesALayoutFailure(t *testing.T) {
	t.Parallel()
	f := newFakeClient("see https://a.io/x for more")
	f.panesErr = errors.New("no layout")
	f.scrollsErr = errors.New("no list")
	a := testApp(t, f, config.Config{})

	p := a.gather(context.Background(), "w1:p1")
	if p.Focused != "w1:p1" {
		t.Fatalf("gather() focused = %q, want w1:p1", p.Focused)
	}
	if len(p.Panes) != 0 || len(p.Scrolls) != 0 {
		t.Fatalf("gather() = %+v, want nothing where the calls failed", p)
	}
}

func TestPrepareStopsWithoutAFocusedPane(t *testing.T) {
	noPaneEnv(t)
	f := newFakeClient("see https://a.io/x for more")
	cfg := config.Load()
	cfg.StateDir = t.TempDir()
	a := testApp(t, f, cfg)

	env := map[string]string{}
	marker, drew := a.prepare(context.Background(), env)
	if marker != nil || drew {
		t.Fatalf("prepare() = %v/%v, want nothing without a pane", marker, drew)
	}
	if len(env) != 0 {
		t.Fatalf("prepare() left %+v on the environment", env)
	}
}

// An unwritable state dir is not fatal: the picker rescans for itself.
func TestPrepareLeavesNoHandoffItCouldNotWrite(t *testing.T) {
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
	themeEnv(t)
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	f := newFakeClient("see https://a.io/x for more")
	cfg := config.Load()
	cfg.StateDir, cfg.TermProgram = dir, "test-unwritable"
	a := testApp(t, f, cfg)

	env := map[string]string{}
	if _, drew := a.prepare(context.Background(), env); drew {
		t.Fatal("prepare() claimed a drawing it could not have made")
	}
	if _, ok := env[handoff.EnvVar]; ok {
		t.Fatalf("prepare() pointed the pane at a handoff it never wrote: %+v", env)
	}
}

func TestPickStopsWhenNothingNamesAPane(t *testing.T) {
	noPaneEnv(t)
	f := newFakeClient("see https://a.io/x for more")
	cfg := config.Load()
	cfg.StateDir = t.TempDir()
	a := testApp(t, f, cfg)

	term, out := pipeTerminal(t, "a\n")
	if code := a.pick(context.Background(), term, theme.Fallback); code != exitCancelled {
		t.Fatalf("pick() = %d, want %d", code, exitCancelled)
	}
	term.Close()
	if got := out.String(); !strings.Contains(got, "no pane") {
		t.Fatalf("pick() said %q, want it to say there was no pane", got)
	}
}

// An unmatched code is a quit, not a failure.
func TestPickCancelsOnACodeThatMatchesNothing(t *testing.T) {
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
	f := newFakeClient("see https://a.io/x for more")
	cfg := config.Load()
	cfg.StateDir = t.TempDir()
	a := testApp(t, f, cfg)

	term, _ := pipeTerminal(t, "zz\n")
	if code := a.pick(context.Background(), term, theme.Fallback); code != exitCancelled {
		t.Fatalf("pick() = %d, want %d", code, exitCancelled)
	}
	term.Close()
}

// Scrolled away between the scan and the pick, the link is gone.
func TestPickFailsWhenTheLinkMovedOffScreen(t *testing.T) {
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
	f := newFakeClient("see https://a.io/x for more")
	f.rescan = map[string][]string{"w1:p1": {"nothing here now"}}
	cfg := config.Load()
	cfg.StateDir = t.TempDir()
	a := testApp(t, f, cfg)

	term, out := pipeTerminal(t, "a\n")
	if code := a.pick(context.Background(), term, theme.Fallback); code != exitFailed {
		t.Fatalf("pick() = %d, want %d", code, exitFailed)
	}
	term.Close()
	if got := out.String(); !strings.Contains(got, "scrolled") {
		t.Fatalf("pick() said %q, want it to say the pane scrolled", got)
	}
}

// browse refuses an unsupported scheme before it launches anything.
func TestPickFailsWhenTheBrowserRefusesTheURL(t *testing.T) {
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")
	f := newFakeClient("see https://a.io/x for more")
	f.activate = herdr.Activation{URL: "gopher://a.io/x"}
	cfg := config.Load()
	cfg.StateDir = t.TempDir()
	a := testApp(t, f, cfg)

	term, out := pipeTerminal(t, "a\n")
	if code := a.pick(context.Background(), term, theme.Fallback); code != exitFailed {
		t.Fatalf("pick() = %d, want %d", code, exitFailed)
	}
	term.Close()
	if got := out.String(); !strings.Contains(got, "failed") {
		t.Fatalf("pick() said %q, want it to report the failure", got)
	}
}

// The popup footprint rides in the handoff, so both processes keep off it.
func TestGatherCarriesThePopupFootprint(t *testing.T) {
	t.Parallel()
	f := newFakeClient("see https://a.io/x for more")
	a := testApp(t, f, config.Config{})
	if got, want := a.gather(context.Background(), "w1:p1").Popup, (herdr.Rect{X: 33, Y: 9, Width: 14, Height: 5}); got != want {
		t.Fatalf("gather().Popup = %+v, want %+v", got, want)
	}
}
