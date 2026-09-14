package hints

import (
	"reflect"
	"testing"

	"github.com/reobin/herdr-link-hints/internal/links"
	"github.com/reobin/herdr-link-hints/internal/overlay"
)

func TestBadgesGroupsByPane(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{Row: 2, Col: 4, Before: 3, Text: "abcd", Pane: "w1:p1"},
		{Row: 9, Col: 0, Text: "ab", Pane: "w1:p2"},
		{Row: 3, Col: 7, Before: 1, Text: "abc", Pane: "w1:p1"},
	}
	codes := []string{"a", "s", "d"}

	all := Badges(found, codes, []int{0, 1, 2}, "")
	want := map[string][]overlay.Badge{
		"w1:p1": {
			{Row: 2, Col: 4, Before: 3, Width: 4, Code: "a"},
			{Row: 3, Col: 7, Before: 1, Width: 3, Code: "d"},
		},
		"w1:p2": {{Row: 9, Col: 0, Width: 2, Code: "s"}},
	}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("Badges() = %+v, want %+v", all, want)
	}
}

// Narrowing fades the hints it rules out instead of dropping them.
func TestBadgesFadesTheRestWhenNarrowing(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{Row: 2, Col: 4, Text: "ab", Pane: "w1:p1"},
		{Row: 9, Col: 0, Text: "ab", Pane: "w1:p1"},
	}
	got := Badges(found, []string{"a", "s"}, []int{1}, "")
	if len(got["w1:p1"]) != 2 {
		t.Fatalf("Badges() = %+v, want both links kept", got)
	}
	if !got["w1:p1"][0].Dim {
		t.Fatal("the ruled-out hint should be dimmed")
	}
	if got["w1:p1"][1].Dim {
		t.Fatal("the matching hint should stay bright")
	}
}

// Narrowing marks the typed prefix on each badge, so the screen shows what
// the readout echoed. A ruled-out badge claims only the runes it shares.
func TestBadgesMarksTheTypedPrefix(t *testing.T) {
	t.Parallel()
	found := []links.Link{
		{Row: 2, Col: 4, Text: "ab", Pane: "w1:p1"},
		{Row: 9, Col: 0, Text: "ab", Pane: "w1:p1"},
	}
	got := Badges(found, []string{"ad", "as"}, []int{1}, "as")
	if got["w1:p1"][0].Typed != 1 {
		t.Fatalf("Badges() ruled-out Typed = %d, want 1", got["w1:p1"][0].Typed)
	}
	if got["w1:p1"][1].Typed != 2 {
		t.Fatalf("Badges() matching Typed = %d, want 2", got["w1:p1"][1].Typed)
	}
	if got["w1:p1"][0].Dim == got["w1:p1"][1].Dim {
		t.Fatal("Badges() should still dim the ruled-out hint")
	}
}

func TestCommonPrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code, typed string
		want        int
	}{
		{"as", "", 0},
		{"as", "a", 1},
		{"as", "as", 2},
		{"as", "asd", 2},
		{"ad", "as", 1},
		{"sd", "as", 0},
	} {
		if got := commonPrefix(tc.code, tc.typed); got != tc.want {
			t.Fatalf("commonPrefix(%q, %q) = %d, want %d", tc.code, tc.typed, got, tc.want)
		}
	}
}
