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

// realScene is a page of scanned links rather than a saturated grid: the
// shape narrowing is actually paid on.
func realScene(badges int) Scene {
	viewport := Size{Cols: 204, Rows: 57}
	placed := make([]Badge, badges)
	for i := range placed {
		placed[i] = Badge{
			Row:    (i * 7) % viewport.Rows,
			Col:    4 + (i*23)%(viewport.Cols-40),
			Before: 4,
			Width:  20,
			Code:   string(rune('a'+i%9)) + string(rune('a'+(i/9)%9)),
		}
	}
	s := scene(placed, viewport)
	s.Cell = Cell{Width: 19, Height: 54}
	return s
}

// The two per-keystroke costs: full frame vs badge layers.
func BenchmarkNarrowByFrame(b *testing.B) {
	s := realScene(150)
	plan, err := NewPlan(s)
	if err != nil {
		b.Fatal(err)
	}
	narrowed := dimAll(s.Badges)
	for i := range 6 {
		narrowed[i].Dim, narrowed[i].Typed = false, 1
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := plan.Frame(narrowed); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNarrowByLayer(b *testing.B) {
	s := realScene(150)
	plan, err := NewPlan(s)
	if err != nil {
		b.Fatal(err)
	}
	narrowed := dimAll(s.Badges)
	for i := range 6 {
		narrowed[i].Dim, narrowed[i].Typed = false, 1
	}
	var matches []int
	for i := range plan.Placed() {
		if !narrowed[plan.Link(i)].Dim {
			matches = append(matches, i)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, i := range matches {
			if _, err := plan.Layer(i, narrowed); err != nil {
				b.Fatal(err)
			}
		}
	}
}
