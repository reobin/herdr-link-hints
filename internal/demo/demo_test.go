package demo

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/overlay"
)

func TestRankedIsDeterministic(t *testing.T) {
	t.Parallel()
	first, second := Ranked(), Ranked()
	if len(first) != 6 || len(second) != 6 {
		t.Fatalf("Ranked() len = %d, %d; want 6, 6", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("Ranked() run 1 [%d] = %+v, run 2 = %+v", i, first[i], second[i])
		}
	}
	if first[0].URL != "https://c.io/x" {
		t.Fatalf("Ranked()[0].URL = %q, want the link nearest the cursor", first[0].URL)
	}
	seen := map[string]bool{}
	for _, c := range Codes() {
		if seen[c] {
			t.Fatalf("Codes() has a duplicate %q", c)
		}
		seen[c] = true
	}
}

func TestGolden(t *testing.T) {
	t.Parallel()
	for name, scene := range Scenes() {
		frame, err := overlay.Render(scene)
		if err != nil {
			t.Fatalf("%s: Render() error: %v", name, err)
		}
		path := filepath.Join("testdata", "golden", name+".png")
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, frame.PNG, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: read golden: %v (run with UPDATE_GOLDEN=1)", name, err)
		}
		if !bytes.Equal(frame.PNG, want) {
			t.Fatalf("%s: rendered %d bytes, golden %d bytes", name, len(frame.PNG), len(want))
		}
	}
}
