// Package scan turns panes into a hint list.
package scan

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/reobin/herdr-link-hints/internal/ansi"
	"github.com/reobin/herdr-link-hints/internal/cells"
	"github.com/reobin/herdr-link-hints/internal/links"
)

// Source is the Herdr slice scanning needs.
type Source interface {
	PaneLines(ctx context.Context, pane string) ([]string, error)
	ObserveOSC8(ctx context.Context, pane string, cols, rows int) ([]ansi.Link, error)
}

// Pane is a pane to scan and its content size.
type Pane struct {
	ID   string
	Cols int
	Rows int
}

// Scanner logs failed reads rather than failing the scan.
type Scanner struct {
	Source      Source
	Log         *slog.Logger
	SkipObserve bool
}

// Links keeps pane order, so codes stay predictable.
func (s *Scanner) Links(ctx context.Context, panes []Pane) []links.Link {
	perPane := make([][]links.Link, len(panes))
	var wg sync.WaitGroup
	for i, pane := range panes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			perPane[i] = s.paneLinks(ctx, pane)
		}()
	}
	wg.Wait()

	var all []links.Link
	for _, part := range perPane {
		all = append(all, part...)
	}
	return all
}

func (s *Scanner) paneLinks(ctx context.Context, pane Pane) []links.Link {
	var (
		visible []string
		hidden  []ansi.Link
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		visible = s.read(ctx, pane.ID)
	}()
	go func() {
		defer wg.Done()
		if s.SkipObserve {
			return
		}
		found, err := s.Source.ObserveOSC8(ctx, pane.ID, pane.Cols, pane.Rows)
		if err != nil {
			s.Log.Debug("observe failed", "pane", pane.ID, "error", err)
			return
		}
		hidden = found
	}()
	wg.Wait()

	if len(visible) == 0 {
		return nil
	}
	// Rejoined here: asking Herdr for unwrapped scrollback scrolls the pane.
	// One URL sweep feeds both carry check and link list.
	matches := links.MatchLines(visible)
	var known map[string]bool
	if carried := carries(visible, matches); len(carried) > 0 {
		known = links.Known(unwrap(visible, pane.Cols))
		guardUnwrap(carried, known, pane.Cols)
	}
	found := links.Merge(visible, links.FromMatches(visible, matches, known), hidden)
	for i := range found {
		found[i].Pane = pane.ID
	}
	return found
}

// unwrap rejoins soft-wrapped lines.
func unwrap(visible []string, cols int) string {
	if cols <= 0 {
		return strings.Join(visible, "\n")
	}
	var out strings.Builder
	for _, line := range visible {
		out.WriteString(line)
		if cells.Width(line) >= cols {
			continue
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// guardUnwrap drops completions only the join could vouch for.
func guardUnwrap(carried []carry, known map[string]bool, cols int) {
	for _, c := range carried {
		if cols <= 0 || cells.Width(c.line) < cols {
			continue
		}
		if links.Clean(c.raw) == c.raw {
			continue
		}
		cont := continuation(c.next)
		if cont == "" {
			continue
		}
		want := links.Clean(c.raw + cont)
		for url := range known {
			if len(url) >= len(want) && strings.HasPrefix(url, want) {
				delete(known, url)
			}
		}
	}
}

// continuation is the next line's first token.
func continuation(next string) string {
	if next == "" {
		return ""
	}
	switch next[0] {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return ""
	}
	if i := strings.IndexAny(next, " \t\n\r\v\f"); i >= 0 {
		return next[:i]
	}
	return next
}

// carry is a raw match running to a non-final line end.
type carry struct {
	line string
	next string
	raw  string
}

// carries mirrors the carry in links.FromLines, matching on raw ends.
func carries(visible []string, matches [][]links.Match) []carry {
	var out []carry
	for i, line := range visible {
		if i+1 >= len(visible) {
			break
		}
		for _, m := range matches[i] {
			if m.End == len(line) {
				out = append(out, carry{line: line, next: visible[i+1], raw: m.Raw})
			}
		}
	}
	return out
}

func (s *Scanner) read(ctx context.Context, pane string) []string {
	text, err := s.Source.PaneLines(ctx, pane)
	if err != nil {
		s.Log.Debug("pane read failed", "pane", pane, "error", err)
	}
	return text
}

// stillThere reports whether a link is still at its cell after scrolling.
func stillThere(visible []string, choice links.Link, row int) bool {
	if choice.Text == "" || row < 0 || row >= len(visible) {
		return false
	}
	line := visible[row]
	// Text links match by whole URL at the cell, not substring.
	if choice.Kind == links.Text {
		for _, m := range links.FindAll(line) {
			if cells.Column(line, m.Start) == choice.Col && links.Normalize(m.URL) == choice.URL {
				return true
			}
		}
		return wrapped(visible, choice, row)
	}
	for at := 0; at < len(line); {
		i := strings.Index(line[at:], choice.Text)
		if i < 0 {
			return false
		}
		if cells.Column(line, at+i) == choice.Col {
			return true
		}
		at += i + len(choice.Text)
	}
	return false
}

// wrapped reports text split by a soft wrap.
func wrapped(visible []string, choice links.Link, row int) bool {
	if row+1 >= len(visible) {
		return false
	}
	line, next := visible[row], visible[row+1]
	if next == "" {
		return false
	}
	// Walk rune boundaries: a byte offset inside a rune mismeasures.
	for at := range line {
		run := line[at:]
		if cells.Column(line, at) != choice.Col || len(run) >= len(choice.Text) || !strings.HasPrefix(choice.Text, run) {
			continue
		}
		rest := choice.Text[len(run):]
		if strings.HasPrefix(next, rest) || strings.HasPrefix(rest, next) {
			return true
		}
	}
	return false
}

// Locate re-resolves a link's cell after scrolling.
func (s *Scanner) Locate(ctx context.Context, choice links.Link, shift int) (row, col int, ok bool) {
	shifted := choice.Row - shift
	visible := s.read(ctx, choice.Pane)
	// Prefer the hinted cell over the first match anywhere.
	if stillThere(visible, choice, shifted) {
		return shifted, choice.Col, true
	}
	if choice.Kind == links.Text {
		for i, line := range visible {
			for _, m := range links.FindAll(line) {
				if links.Normalize(m.URL) == choice.URL {
					return i, cells.Column(line, m.Start), true
				}
			}
		}
	}
	// OSC8 targets need anchor search; text links already swept above.
	if choice.Kind == links.OSC8 && choice.Text != "" {
		for i, line := range visible {
			if at := strings.Index(line, choice.Text); at >= 0 {
				return i, cells.Column(line, at), true
			}
		}
	}
	return 0, 0, false
}
