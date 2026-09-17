package handoff

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

func TestHandoffRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Payload{
		Focused:   "w1:p1",
		Panes:     []herdr.Pane{{ID: "w1:p1", Width: 204, Height: 57}},
		Scrolls:   map[string]herdr.Scroll{"w1:p1": {Offset: 3, ViewportRows: 57}},
		Infos:     map[string]herdr.Graphics{"w1:p1": {PaneVisible: true, CellWidthPx: 19, CellHeightPx: 54}},
		HasColors: true,
		Found:     []links.Link{{URL: "https://a.io/x", Text: "x", Row: 2, Col: 4, Pane: "w1:p1"}},
		Drawn:     []string{"w1:p1"},
	}

	path, err := Write(dir, want)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, ok := Read(path, discardLogger())
	if !ok {
		t.Fatal("Read rejected a payload just written")
	}
	if got.Focused != want.Focused || len(got.Found) != 1 || got.Found[0].URL != want.Found[0].URL {
		t.Fatalf("Read() = %+v, want the payload back", got)
	}
	if got.Infos["w1:p1"].CellWidthPx != 19 || got.Scrolls["w1:p1"].Offset != 3 {
		t.Fatalf("Read() lost pane state: %+v", got)
	}
	// Consumed: no second pickup.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the payload should be removed once read")
	}
}

// Untrusted payloads fall back to scanning.
func TestHandoffRejectsWhatItCannotTrust(t *testing.T) {
	dir := t.TempDir()
	log := discardLogger()

	if _, ok := Read("", log); ok {
		t.Fatal("no path should mean no handoff")
	}
	if _, ok := Read(filepath.Join(dir, "absent.json"), log); ok {
		t.Fatal("a missing file should mean no handoff")
	}

	torn := filepath.Join(dir, "torn.json")
	if err := os.WriteFile(torn, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(torn, log); ok {
		t.Fatal("unparseable json should mean no handoff")
	}

	stale := filepath.Join(dir, "stale.json")
	body, err := json.Marshal(Payload{Focused: "w1:p1", WroteAt: time.Now().Add(-2 * ttl)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(stale, log); ok {
		t.Fatal("a payload older than the ttl belongs to a run that died")
	}
}

// Abandoned payloads must not linger.
func TestHandoffSweepsWhatNobodyCameFor(t *testing.T) {
	dir := t.TempDir()

	abandoned := filepath.Join(dir, "handoff-999999.json")
	if err := os.WriteFile(abandoned, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * ttl)
	if err := os.Chtimes(abandoned, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := Write(dir, Payload{Focused: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("a payload past the ttl should have been swept")
	}
}
