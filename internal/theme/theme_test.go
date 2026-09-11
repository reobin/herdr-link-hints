package theme

import (
	"image/color"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		reply string
		key   string
		want  color.RGBA
	}{
		{"background, four digits", "\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\", KeyBackground, color.RGBA{R: 0x1E, G: 0x1E, B: 0x2E, A: 0xFF}},
		{"foreground, two digits", "\x1b]10;rgb:cd/d6/f4\x1b\\", KeyForeground, color.RGBA{R: 0xCD, G: 0xD6, B: 0xF4, A: 0xFF}},
		{"a palette entry keeps its index", "\x1b]4;3;rgb:f9e2/af00/0000\x1b\\", KeyAccent, color.RGBA{R: 0xF9, G: 0xAE, B: 0x00, A: 0xFF}},
		{"terminated by bel", "\x1b]11;rgb:00/00/00\a", KeyBackground, color.RGBA{A: 0xFF}},
		{"one digit scales to full range", "\x1b]11;rgb:f/f/f\x1b\\", KeyBackground, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key, got, ok := Parse(tc.reply)
			if !ok {
				t.Fatalf("Parse(%q) failed", tc.reply)
			}
			if key != tc.key || got != tc.want {
				t.Fatalf("Parse(%q) = %q, %+v; want %q, %+v", tc.reply, key, got, tc.key, tc.want)
			}
		})
	}
}

func TestParseRejectsWhatIsNotAColourReport(t *testing.T) {
	t.Parallel()
	for _, reply := range []string{
		"",
		"rgb:11/22/33",
		"\x1b]11;#112233\x1b\\",
		"\x1b]11;rgb:11/22\x1b\\",
		"\x1b]11;rgb:11/22/zz\x1b\\",
		"\x1b]11;rgb:11/22/333333\x1b\\",
	} {
		if _, _, ok := Parse(reply); ok {
			t.Errorf("Parse(%q) succeeded", reply)
		}
	}
}

func TestSetFilesEachColour(t *testing.T) {
	t.Parallel()
	red := color.RGBA{R: 0xFF, A: 0xFF}
	var colors Colors
	colors.Set(KeyAccent, red)
	colors.Set("9", color.RGBA{G: 0xFF, A: 0xFF})
	if colors.Accent != red {
		t.Fatalf("Accent = %+v, want %+v", colors.Accent, red)
	}
	if colors.Foreground != (color.RGBA{}) || colors.Background != (color.RGBA{}) {
		t.Fatalf("an unknown key touched the other colours: %+v", colors)
	}
}

func TestFallbackIsOpaqueBlackAndWhite(t *testing.T) {
	t.Parallel()
	got := Fallback()
	if got.Background != (color.RGBA{A: 0xFF}) {
		t.Fatalf("Background = %+v", got.Background)
	}
	if got.Foreground != (color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}) {
		t.Fatalf("Foreground = %+v", got.Foreground)
	}
}

func TestMix(t *testing.T) {
	t.Parallel()
	black := color.RGBA{A: 0xFF}
	white := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	if got := Mix(white, black, 0); got != white {
		t.Fatalf("Mix(.., 0) = %+v, want the first colour", got)
	}
	if got := Mix(white, black, 1); got != black {
		t.Fatalf("Mix(.., 1) = %+v, want the second colour", got)
	}
	if got := Mix(white, black, 0.5); got.R != 0x80 || got.A != 0xFF {
		t.Fatalf("Mix(.., 0.5) = %+v", got)
	}
}

// image/png un-premultiplies a palette entry on the way out, so an alpha
// that is not premultiplied comes back as the wrong colour.
func TestFadeIsPremultiplied(t *testing.T) {
	t.Parallel()
	got := Fade(color.RGBA{R: 0xFF, G: 0x80, B: 0x00, A: 0xFF}, 0x80)
	if got.A != 0x80 || got.R != 0x80 || got.G != 0x40 || got.B != 0x00 {
		t.Fatalf("Fade() = %+v", got)
	}
	if got.R > got.A {
		t.Fatalf("Fade() = %+v is not a valid premultiplied colour", got)
	}
}

func TestQuery(t *testing.T) {
	t.Parallel()
	if got := Query(KeyAccent); got != "\x1b]4;3;?\x1b\\" {
		t.Fatalf("Query(%q) = %q", KeyAccent, got)
	}
}
