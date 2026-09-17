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

// Codes grow shortest first.
func TestCodesGrowShortestFirst(t *testing.T) {
	t.Parallel()
	base := len([]rune(DefaultAlphabet))
	codes := Codes(base+1, DefaultAlphabet)
	if len(codes) != base+1 {
		t.Fatalf("Codes(%d) returned %d codes", base+1, len(codes))
	}
	singles, doubles := 0, 0
	for _, code := range codes {
		switch len([]rune(code)) {
		case 1:
			singles++
		case 2:
			doubles++
		default:
			t.Fatalf("Codes(%d) produced %q, want at most width 2", base+1, code)
		}
	}
	if singles != base-1 || doubles != 2 {
		t.Fatalf("Codes(%d) = %d singles and %d doubles, want %d and 2", base+1, singles, doubles, base-1)
	}
	// The earliest characters stay short: the expansion starts at the end
	// of the alphabet, so its last character is the first leaf sacrificed.
	runes := []rune(DefaultAlphabet)
	want := make([]string, 0, base+1)
	for _, r := range runes[:len(runes)-1] {
		want = append(want, string(r))
	}
	last := string(runes[len(runes)-1])
	want = append(want, last+string(runes[0]), last+string(runes[1]))
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("Codes(%d) = %+v, want %+v", base+1, codes, want)
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

// No code may prefix another: that is what lets a fully typed code select
// without Enter.
func TestCodesArePrefixFree(t *testing.T) {
	t.Parallel()
	codes := Codes(500, DefaultAlphabet)
	for i, a := range codes {
		for j, b := range codes {
			if i != j && strings.HasPrefix(b, a) {
				t.Fatalf("code %q is a prefix of %q", a, b)
			}
		}
	}
}
