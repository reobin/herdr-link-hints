// Package scan turns a set of panes into a hint list.
package scan

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/reobin/herdr-link-hints/internal/ansi"
	"github.com/reobin/herdr-link-hints/internal/cells"
	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
)

// Source is the slice of Herdr that scanning needs.
type Source interface {
	PaneLines(ctx context.Context, pane, source string, lines int) ([]string, error)
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
		visible = s.read(ctx, pane.ID, herdr.SourceVisible, 0)
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
	var known map[string]bool
	if needsUnwrapped(visible) {
		known = links.Known(strings.Join(unwrap(visible, pane.Cols), "\n"))
		guardUnwrap(visible, known, pane.Cols)
	}
	found := links.Merge(visible, links.FromLines(visible, known), hidden)
	for i := range found {
		found[i].Pane = pane.ID
	}
	return found
}

// unwrap rejoins the lines the snapshot broke at the pane's edge: a line
// filled to the last cell is a soft wrap and continues into the next.
func unwrap(visible []string, cols int) []string {
	if cols <= 0 {
		return visible
	}
	var out []string
	joined := ""
	for _, line := range visible {
		joined += line
		if cells.Width(line) >= cols {
			continue
		}
		out = append(out, joined)
		joined = ""
	}
	if joined != "" {
		out = append(out, joined)
	}
	return out
}

// guardUnwrap drops a completion only unwrap's own join could vouch for: a
// carried match ending in trailing punctuation. Whether that mark ends prose
// or continues the URL is a guess, and the joined text built from that guess
// must not confirm it. Structural wraps carry no such mark and are kept.
// Only soft-wrapped lines can join, mirroring unwrap, so a short line never
// nukes an unrelated completion sharing its prefix.
func guardUnwrap(visible []string, known map[string]bool, cols int) {
	for _, c := range carries(visible) {
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

func carries(visible []string) []carry {
	var out []carry
	for i, line := range visible {
		if i+1 >= len(visible) {
			break
		}
		for _, m := range links.FindAll(line) {
			if m.End == len(line) {
				out = append(out, carry{line: line, next: visible[i+1], raw: m.Raw})
			}
		}
	}
	return out
}

// needsUnwrapped mirrors the carry in links.FromLines. It matches on the
// raw end, not the cleaned one: whether a trailing "." ends a URL or ends
// a sentence is the very thing only scrollback can settle.
func needsUnwrapped(visible []string) bool {
	return len(carries(visible)) > 0
}

func (s *Scanner) read(ctx context.Context, pane, source string, lines int) []string {
	text, err := s.Source.PaneLines(ctx, pane, source, lines)
	if err != nil {
		s.Log.Debug("pane read failed", "pane", pane, "source", source, "error", err)
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

// Locate re-resolves a link's cell just before opening it: output may have
// scrolled while the user was typing.
func (s *Scanner) Locate(ctx context.Context, choice links.Link, shift int) (row, col int, ok bool) {
	shifted := choice.Row - shift
	if shift != 0 && shifted >= 0 && choice.Kind == links.Text {
		return shifted, choice.Col, true
	}

	visible := s.read(ctx, choice.Pane, herdr.SourceVisible, 0)
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
	// left to search for.
	if choice.Text != "" {
		for i, line := range visible {
			if at := strings.Index(line, choice.Text); at >= 0 {
				return i, cells.Column(line, at), true
			}
		}
	}
	if shifted >= 0 {
		return shifted, choice.Col, true
	}
	return 0, 0, false
}
