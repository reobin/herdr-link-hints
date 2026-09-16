package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// Not parallel: the handoff path is an environment variable.
func TestHandoffRoundTrip(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	want := prepared{
		Focused:   "w1:p1",
		Panes:     []herdr.Pane{{ID: "w1:p1", Width: 204, Height: 57}},
		Scrolls:   map[string]herdr.Scroll{"w1:p1": {Offset: 3, ViewportRows: 57}},
		Infos:     map[string]herdr.Graphics{"w1:p1": {PaneVisible: true, CellWidthPx: 19, CellHeightPx: 54}},
		HasColors: true,
		Found:     []links.Link{{URL: "https://a.io/x", Text: "x", Row: 2, Col: 4, Pane: "w1:p1"}},
		Drawn:     []string{"w1:p1"},
	}

	path, err := writeHandoff(want)
	if err != nil {
		t.Fatalf("writeHandoff: %v", err)
	}
	got, ok := readHandoff(path, discardLogger())
	if !ok {
		t.Fatal("readHandoff rejected a payload just written")
	}
	if got.Focused != want.Focused || len(got.Found) != 1 || got.Found[0].URL != want.Found[0].URL {
		t.Fatalf("readHandoff() = %+v, want the payload back", got)
	}
	if got.Infos["w1:p1"].CellWidthPx != 19 || got.Scrolls["w1:p1"].Offset != 3 {
		t.Fatalf("readHandoff() lost pane state: %+v", got)
	}
	// Consumed, so a second picker cannot pick up a scan meant for the first.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the payload should be removed once read")
	}
}

// Anything the picker cannot vouch for means scanning for itself, which is
// what it did before the action process took the work over.
func TestHandoffRejectsWhatItCannotTrust(t *testing.T) {
	dir := t.TempDir()
	log := discardLogger()

	if _, ok := readHandoff("", log); ok {
		t.Fatal("no path should mean no handoff")
	}
	if _, ok := readHandoff(filepath.Join(dir, "absent.json"), log); ok {
		t.Fatal("a missing file should mean no handoff")
	}

	torn := filepath.Join(dir, "torn.json")
	if err := os.WriteFile(torn, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readHandoff(torn, log); ok {
		t.Fatal("unparseable json should mean no handoff")
	}

	stale := filepath.Join(dir, "stale.json")
	body, err := json.Marshal(prepared{Focused: "w1:p1", WroteAt: time.Now().Add(-2 * handoffTTL)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readHandoff(stale, log); ok {
		t.Fatal("a payload older than the ttl belongs to a run that died")
	}
}

// The state dir is Herdr's, so a run whose picker never opened must not
// leave its payload there for good.
func TestHandoffSweepsWhatNobodyCameFor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)

	abandoned := filepath.Join(dir, "handoff-999999.json")
	if err := os.WriteFile(abandoned, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * handoffTTL)
	if err := os.Chtimes(abandoned, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := writeHandoff(prepared{Focused: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("a payload past the ttl should have been swept")
	}
}
