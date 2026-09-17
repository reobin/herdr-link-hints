package marks

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/overlay"
)

// clearRecorder stands in for Herdr, remembering what came down.
type clearRecorder struct {
	mu      sync.Mutex
	cleared []string
}

func (c *clearRecorder) SetGraphics(context.Context, herdr.Frame) error { return nil }

func (c *clearRecorder) ClearGraphics(_ context.Context, pane, layer string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleared = append(c.cleared, pane+" "+layer)
	return nil
}

func (c *clearRecorder) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.cleared)
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestReclaimClearsWhatAKilledRunLeft(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body, err := json.Marshal(map[string][]string{
		"w1:p1": {"link-hints", "link-hints-dim", "link-hints-3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "layers-999999.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	client := &clearRecorder{}
	Reclaim(context.Background(), client, discard(), dir)

	got := client.seen()
	slices.Sort(got)
	want := []string{"w1:p1 link-hints", "w1:p1 link-hints-3", "w1:p1 link-hints-dim"}
	if !slices.Equal(got, want) {
		t.Fatalf("Reclaim() cleared %q, want %q", got, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a reclaimed trail should be removed, or the next run redoes the work")
	}
}

func TestReclaimLeavesItsOwnTrailAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	trail := OpenTrail(dir)
	trail.mark("w1:p1", "link-hints")

	client := &clearRecorder{}
	Reclaim(context.Background(), client, discard(), dir)

	if got := client.seen(); len(got) != 0 {
		t.Fatalf("Reclaim() cleared %q, want it to skip the live run's own trail", got)
	}
	if _, err := os.Stat(trail.path); err != nil {
		t.Fatalf("Reclaim() removed the live run's own trail: %v", err)
	}
}

// The normal case: nothing left behind.
func TestReclaimIsQuietWithNothingToDo(t *testing.T) {
	t.Parallel()
	client := &clearRecorder{}
	Reclaim(context.Background(), client, discard(), t.TempDir())
	Reclaim(context.Background(), client, discard(), "")
	Reclaim(context.Background(), client, discard(), filepath.Join(t.TempDir(), "absent"))
	if got := client.seen(); len(got) != 0 {
		t.Fatalf("Reclaim() cleared %q with no trail to follow", got)
	}
}

// A torn trail is dropped rather than retried forever.
func TestReclaimDropsATornTrail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "layers-999998.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	Reclaim(context.Background(), &clearRecorder{}, discard(), dir)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("an unparseable trail should be removed")
	}
}

func TestTrailIsGoneAfterACleanClear(t *testing.T) {
	t.Parallel()
	_, socket := startGraphicsServer(t)
	dir := t.TempDir()
	trail := OpenTrail(dir)
	m := testMarker(t, socket, "w1:p1")
	m.trail = trail
	ctx := context.Background()

	m.Draw(ctx, map[string][]overlay.Badge{"w1:p1": testBadges(2)})
	if _, err := os.Stat(trail.path); err != nil {
		t.Fatalf("drawing left no trail: %v", err)
	}
	m.Clear(ctx)
	if _, err := os.Stat(trail.path); !os.IsNotExist(err) {
		t.Fatal("a clean Clear should take its own trail with it")
	}
}

func TestTrailWithoutAStateDirectoryIsInert(t *testing.T) {
	t.Parallel()
	var trail *Trail
	if got := OpenTrail(""); got != nil {
		t.Fatalf("OpenTrail(\"\") = %+v, want nil", got)
	}
	trail.mark("w1:p1", "link-hints")
	trail.done()
}
