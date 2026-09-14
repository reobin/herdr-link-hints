package overlay

import (
	"testing"
	"time"

	"github.com/reobin/herdr-link-hints/internal/theme"
)

const renderBudget = 2 * time.Second

func fullScreenScene() Scene {
	viewport := Size{Cols: 204, Rows: 57}
	var badges []Badge
	for row := range viewport.Rows {
		for col := 0; col < viewport.Cols; col += 6 {
			badges = append(badges, Badge{Row: row, Col: col, Width: 4, Code: "as"})
		}
	}
	return Scene{Badges: badges, Colors: theme.Fallback(), Cell: Cell{Width: 19, Height: 54}, Viewport: viewport}
}

func BenchmarkRenderFullScreen(b *testing.B) {
	scene := fullScreenScene()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Render(scene); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRenderBudget(t *testing.T) {
	t.Parallel()
	start := time.Now()
	if _, err := Render(fullScreenScene()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > renderBudget {
		t.Fatalf("Render() took %v, over the %v budget", took, renderBudget)
	}
}
