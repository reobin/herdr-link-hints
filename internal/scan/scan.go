// Package scan turns a set of panes into a hint list.
package scan

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/reobin/herdr-link-hints/internal/ansi"
	"github.com/reobin/herdr-link-hints/internal/herdr"
	"github.com/reobin/herdr-link-hints/internal/links"
)

const scrollbackLines = 200

// Source is the slice of Herdr that scanning needs.
type Source interface {
	PaneLines(ctx context.Context, pane, source string, lines int) ([]string, error)
	ObserveOSC8(ctx context.Context, pane string) ([]ansi.Link, error)
}

// Scanner logs a failed read rather than failing the scan: hints for three
// panes out of four still beat no hints at all.
type Scanner struct {
	Source      Source
	Log         *slog.Logger
	SkipObserve bool
}

// Links keeps the given pane order, so hint codes stay predictable.
func (s *Scanner) Links(ctx context.Context, panes []string) []links.Link {
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
	return links.Dedupe(all)
}

func (s *Scanner) paneLinks(ctx context.Context, pane string) []links.Link {
	var (
		visible []string
		hidden  []ansi.Link
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		visible = s.read(ctx, pane, herdr.SourceVisible, 0)
	}()
	go func() {
		defer wg.Done()
		if s.SkipObserve {
			return
		}
		found, err := s.Source.ObserveOSC8(ctx, pane)
		if err != nil {
			s.Log.Debug("observe failed", "pane", pane, "error", err)
			return
		}
		hidden = found
	}()
	wg.Wait()

	if len(visible) == 0 {
		return nil
	}
	// Viewport-only: scrollback is fetched only for a wrapped URL.
	var known map[string]bool
	if needsUnwrapped(visible) {
		known = links.Known(strings.Join(s.read(ctx, pane, herdr.SourceUnwrapped, scrollbackLines), "\n"))
	}
	found := links.Merge(links.FromLines(visible, known), hidden)
	for i := range found {
		found[i].Pane = pane
	}
	return found
}

// needsUnwrapped mirrors the carry in links.FromLines, which is when the
// scrollback index is consulted. It matches on the raw end, not the
// cleaned one: whether a trailing "." ends a URL or ends a sentence is
// the very thing only scrollback can settle.
func needsUnwrapped(visible []string) bool {
	for i, line := range visible {
		if i+1 >= len(visible) {
			break
		}
		for _, m := range links.FindAll(line) {
			if m.End == len(line) {
				return true
			}
		}
	}
	return false
}

func (s *Scanner) read(ctx context.Context, pane, source string, lines int) []string {
	text, err := s.Source.PaneLines(ctx, pane, source, lines)
	if err != nil {
		s.Log.Debug("pane read failed", "pane", pane, "source", source, "error", err)
	}
	return text
}

// Locate re-resolves a link's cell just before opening it, because output
// may have scrolled while the user was typing. shift accounts for new
// lines; a fresh read confirms the target is still there.
func (s *Scanner) Locate(ctx context.Context, choice links.Link, shift int) (row, col int, ok bool) {
	shifted := choice.Row - shift
	if shift != 0 && shifted >= 0 && choice.Kind == links.Text {
		return shifted, choice.Col, true
	}

	visible := s.read(ctx, choice.Pane, herdr.SourceVisible, 0)
	if choice.Kind == links.Text {
		for i, line := range visible {
			for _, m := range links.FindAll(line) {
				if links.Normalize(m.URL) == choice.URL {
					return i, m.Start, true
				}
			}
		}
	}
	// An OSC 8 target is nowhere in the text, so its anchor is all that is
	// left to search for.
	if choice.Text != "" {
		for i, line := range visible {
			if at := strings.Index(line, choice.Text); at >= 0 {
				return i, at, true
			}
		}
	}
	if shifted >= 0 {
		return shifted, choice.Col, true
	}
	return 0, 0, false
}
