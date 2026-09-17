// Package scan turns a set of panes into a hint list.
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

// Source is the slice of Herdr that scanning needs.
type Source interface {
	PaneLines(ctx context.Context, pane string) ([]string, error)
	ObserveOSC8(ctx context.Context, pane string, cols, rows int) ([]ansi.Link, error)
}

// Pane is a pane to scan and the size its content is laid out in, which is
// what the observe stream must be rendered at for its coordinates to mean
// anything.
type Pane struct {
	ID   string
	Cols int
	Rows int
}

// Scanner logs a failed read rather than failing the scan.
type Scanner struct {
	Source      Source
	Log         *slog.Logger
	SkipObserve bool
}

// Links keeps the given pane order, so hint codes stay predictable.
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
	// Rejoined here rather than read back from Herdr: asking for unwrapped
	// scrollback makes Herdr scroll the pane to answer, and the jump is
	// visible.
	// One sweep of the URL pattern feeds both the carry check and the link
	// list; it used to run once for each and again inside guardUnwrap.
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

// unwrap rejoins the lines the snapshot broke at the pane's edge: a line
// filled to the last cell is a soft wrap and continues into the next. The
// result is the joined text its only caller wants, built in one buffer
// rather than by growing a string per line.
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

// guardUnwrap drops a completion only unwrap's own join could vouch for: a
// carried match ending in trailing punctuation. Whether that mark ends prose
// or continues the URL is a guess, and the joined text built from that guess
// must not confirm it. Structural wraps carry no such mark and are kept.
// Only soft-wrapped lines can join, mirroring unwrap, so a short line never
// nukes an unrelated completion sharing its prefix.
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

// continuation is the next line's first token, the only text unwrap's join
// could fuse onto the carry. A blank or indented next line breaks the run,
// so there is no joined artifact to drop.
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

// carry is a raw match running to the end of a non-final line, the case
// links.FromLines would attempt to complete.
type carry struct {
	line string
	next string
	raw  string
}

// carries mirrors the carry in links.FromLines. It matches on the raw end,
// not the cleaned one: whether a trailing "." ends a URL or ends a sentence
// is the very thing only scrollback can settle.
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

// stillThere reports whether a link's own text is still at the cell it was
// hinted on, once shift has moved it to row.
func stillThere(visible []string, choice links.Link, row int) bool {
	if choice.Text == "" || row < 0 || row >= len(visible) {
		return false
	}
	line := visible[row]
	// A text link is confirmed by the whole URL the cell starts, not by
	// finding its text there: the picked URL is a prefix of every longer one
	// sharing it, and a substring match would settle for that instead.
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

// wrapped reports whether choice.Text is on screen as the soft wrap it was
// read as: the run reaching this line's end at the link's own column, with
// the rest of it resuming the next line. A URL completed across the edge is
// never on one line whole, so that pair is all there is to confirm the cell
// against. A run with nothing under it is a URL that really is that short,
// which is the rule the scan itself joins by.
func wrapped(visible []string, choice links.Link, row int) bool {
	if row+1 >= len(visible) {
		return false
	}
	line, next := visible[row], visible[row+1]
	if next == "" {
		return false
	}
	// Ranging the string walks rune boundaries: a byte offset inside a rune
	// measures as a column of its own, which can reach choice.Col before the
	// boundary that really sits there.
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

// Locate re-resolves a link's cell just before opening it: output may have
// scrolled while the user was typing.
func (s *Scanner) Locate(ctx context.Context, choice links.Link, shift int) (row, col int, ok bool) {
	shifted := choice.Row - shift
	visible := s.read(ctx, choice.Pane)
	// The same anchor can sit in several places, so the cell the hint was
	// drawn on beats the first match anywhere.
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
	// An OSC 8 target is nowhere in the text, so its anchor is all that is
	// left to search for. A text link is its own anchor, so the sweep above
	// already covers it, and a bare substring search here would settle for a
	// longer URL that merely contains it.
	if choice.Kind == links.OSC8 && choice.Text != "" {
		for i, line := range visible {
			if at := strings.Index(line, choice.Text); at >= 0 {
				return i, cells.Column(line, at), true
			}
		}
	}
	return 0, 0, false
}
