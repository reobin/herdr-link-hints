package overlay

import (
	"testing"
	"time"
)

const renderBudget = 2 * time.Second * budgetFactor

// fullScreenScene is the worst case a real pane can produce: a badge every
// six columns on every row of a large viewport with tall cells.
func fullScreenScene() Scene {
	viewport := Size{Cols: 204, Rows: 57}
	var badges []Badge
	for row := range viewport.Rows {
		for col := 0; col < viewport.Cols; col += 6 {
			badges = append(badges, Badge{Row: row, Col: col, Width: 4, Code: "as"})
		}
	}
	full := scene(badges, viewport)
	full.Cell = Cell{Width: 19, Height: 54}
	return full
}

func BenchmarkRenderFullScreen(b *testing.B) {
	scene := fullScreenScene()
	for b.Loop() {
		if _, err := Render(scene); err != nil {
			b.Fatal(err)
		}
	}
}

// Wall-clock, so it runs alone rather than racing the parallel tests.
func TestRenderBudget(t *testing.T) {
	start := time.Now()
	if _, err := Render(fullScreenScene()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > renderBudget {
		t.Fatalf("Render() took %v, over the %v budget", took, renderBudget)
	}
}
