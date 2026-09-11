package hints

import (
	"reflect"
	"strings"
	"testing"
)

func TestCodes(t *testing.T) {
	t.Parallel()
	if got := Codes(3, DefaultAlphabet); !reflect.DeepEqual(got, []string{"a", "s", "d"}) {
		t.Fatalf("Codes(3) = %+v", got)
	}
	if got := Codes(0, DefaultAlphabet); got != nil {
		t.Fatalf("Codes(0) = %+v", got)
	}
	if got := Codes(5, "a"); got != nil {
		t.Fatalf("Codes with a one-character alphabet = %+v", got)
	}
}

// Widths at and around an exact power of the alphabet size are where
// float-based arithmetic goes wrong.
func TestCodesWidthAtPowerBoundaries(t *testing.T) {
	t.Parallel()
	base := len(DefaultAlphabet)
	for _, tc := range []struct{ n, width int }{
		{base, 1},
		{base + 1, 2},
		{base * base, 2},
		{base*base + 1, 3},
		{base * base * base, 3},
	} {
		codes := Codes(tc.n, DefaultAlphabet)
		if len(codes) != tc.n {
			t.Fatalf("Codes(%d) returned %d codes", tc.n, len(codes))
		}
		for _, code := range codes {
			if len(code) != tc.width {
				t.Fatalf("Codes(%d) produced %q, want width %d", tc.n, code, tc.width)
			}
		}
	}
}

func TestCodesAreDistinctAndTypeable(t *testing.T) {
	t.Parallel()
	codes := Codes(500, DefaultAlphabet)
	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if seen[code] {
			t.Fatalf("duplicate code %q", code)
		}
		seen[code] = true
		for _, ch := range code {
			if !strings.ContainsRune(DefaultAlphabet, ch) {
				t.Fatalf("code %q uses %q, which is outside the alphabet", code, ch)
			}
		}
	}
}
