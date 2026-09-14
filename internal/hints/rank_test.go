package hints

import (
	"reflect"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/links"
)

func rankedURLs(found []links.Link) []string {
	urls := make([]string, len(found))
	for i, link := range found {
		urls[i] = link.URL
	}
	return urls
}

func TestRankPutsTheFocusedPaneFirst(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{URL: "https://back.io/", Row: 20, Pane: "w1:p2"},
		{URL: "https://focus.io/", Row: 0, Pane: "w1:p1"},
	}
	got := rankedURLs(Rank(found, "w1:p1", map[string]int{"w1:p1": 23, "w1:p2": 23}))
	if want := []string{"https://focus.io/", "https://back.io/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Rank() = %+v, want %+v", got, want)
	}
}

func TestRankPrefersNearerTheCursor(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{URL: "https://top.io/", Row: 2, Pane: "w1:p1"},
		{URL: "https://bottom.io/", Row: 20, Pane: "w1:p1"},
		{URL: "https://middle.io/", Row: 12, Pane: "w1:p1"},
	}
	got := rankedURLs(Rank(found, "w1:p1", map[string]int{"w1:p1": 23}))
	want := []string{"https://bottom.io/", "https://middle.io/", "https://top.io/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rank() = %+v, want %+v", got, want)
	}
}

func TestRankKeepsScanOrderAmongTies(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{URL: "https://first.io/", Row: 5, Col: 0, Pane: "w1:p1"},
		{URL: "https://second.io/", Row: 5, Col: 40, Pane: "w1:p1"},
	}
	got := rankedURLs(Rank(found, "w1:p1", map[string]int{"w1:p1": 23}))
	want := []string{"https://first.io/", "https://second.io/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rank() = %+v, want %+v", got, want)
	}
}

func TestRankHandlesEmptyAndUnknownPanes(t *testing.T) {
	t.Parallel()
	if got := Rank(nil, "w1:p1", nil); len(got) != 0 {
		t.Fatalf("Rank(nil) = %+v, want empty", got)
	}
	found := []links.Link{
		{URL: "https://old.io/", Row: 1, Pane: "w1:p9"},
		{URL: "https://new.io/", Row: 9, Pane: "w1:p9"},
	}
	got := rankedURLs(Rank(found, "w1:p1", nil))
	if want := []string{"https://new.io/", "https://old.io/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Rank() = %+v, want %+v", got, want)
	}
}

func TestRankLeavesTheInputAlone(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{URL: "https://top.io/", Row: 0, Pane: "w1:p1"},
		{URL: "https://bottom.io/", Row: 20, Pane: "w1:p1"},
	}
	Rank(found, "w1:p1", map[string]int{"w1:p1": 23})
	if found[0].URL != "https://top.io/" {
		t.Fatalf("Rank() reordered its input: %+v", rankedURLs(found))
	}
}
