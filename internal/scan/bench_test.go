package scan

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// paneCols is the width benchPane was captured at, so a line filled to it
// reads as the soft wrap it was on screen.
const paneCols = 204

// benchPane is a real 204x57 pane with no URLs: the common scan case.
func benchPane(tb testing.TB) []string {
	tb.Helper()
	raw, err := os.ReadFile("testdata/pane.txt")
	if err != nil {
		tb.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) < 57 {
		lines = append(lines, "")
	}
	return lines[:57]
}

// paneWithLinks spreads n links across the pane's rows.
func paneWithLinks(tb testing.TB, n int) []string {
	lines := append([]string(nil), benchPane(tb)...)
	for i := range lines {
		if len(lines[i]) > 60 {
			lines[i] = lines[i][:60]
		}
	}
	for i := range n {
		row := i % len(lines)
		lines[row] += fmt.Sprintf("  https://example.com/repo/issues/%d?ref=hints&n=%d", i, i)
	}
	return lines
}

func benchmarkPaneLinks(b *testing.B, lines []string) {
	scanner := newScanner(&fakeSource{text: paneText("w1:p1", lines...)})
	scanner.SkipObserve = true
	pane := Pane{ID: "w1:p1", Cols: paneCols, Rows: len(lines)}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if got := scanner.paneLinks(ctx, pane); len(got) == 0 {
			b.Fatal("paneLinks found nothing at all")
		}
	}
}

func BenchmarkPaneLinks(b *testing.B) { benchmarkPaneLinks(b, paneWithLinks(b, 1)) }

func BenchmarkPaneLinksDense(b *testing.B) { benchmarkPaneLinks(b, paneWithLinks(b, 60)) }

// BenchmarkUnwrapWrapped is the shape unwrap is worst at: every line filled
// to the last cell, so the whole pane joins into one string.
func BenchmarkUnwrapWrapped(b *testing.B) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = strings.Repeat("x", paneCols)
	}
	b.ReportAllocs()
	for b.Loop() {
		if got := unwrap(lines, paneCols); len(got) == 0 {
			b.Fatal("unwrap returned nothing")
		}
	}
}
