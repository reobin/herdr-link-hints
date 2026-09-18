package herdr

import "testing"

// Calibrated against a live 206x59 surface and the default 14x5 popup,
// mirroring Herdr's resolve_popup_geometry.
func TestPopupRect(t *testing.T) {
	t.Parallel()
	surface := Rect{Width: 206, Height: 59}
	cases := []struct {
		name          string
		area          Rect
		width, height string
		want          Rect
		ok            bool
	}{
		{"default picker size", surface, "14", "5", Rect{X: 96, Y: 27, Width: 14, Height: 5}, true},
		{"blank sizes take half the surface", surface, "", "", Rect{X: 51, Y: 15, Width: 103, Height: 29}, true},
		{"percentages", surface, "50%", "25%", Rect{X: 51, Y: 22, Width: 103, Height: 14}, true},
		{"too small grows to the minimum", surface, "2", "1", Rect{X: 100, Y: 27, Width: 6, Height: 4}, true},
		{"too large shrinks to the surface", surface, "999", "999", Rect{Width: 206, Height: 59}, true},
		{"an offset surface keeps its offset", Rect{X: 10, Y: 2, Width: 100, Height: 40}, "14", "5", Rect{X: 53, Y: 19, Width: 14, Height: 5}, true},
		{"a refused value falls to the default", surface, "wide", "5", Rect{X: 51, Y: 27, Width: 103, Height: 5}, true},
		{"a surface under the minimum cannot hold a popup", Rect{Width: 5, Height: 3}, "14", "5", Rect{}, false},
		{"no surface", Rect{}, "14", "5", Rect{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := PopupRect(tc.area, tc.width, tc.height)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("PopupRect(%+v, %q, %q) = %+v, %v; want %+v, %v", tc.area, tc.width, tc.height, got, ok, tc.want, tc.ok)
			}
		})
	}
}
