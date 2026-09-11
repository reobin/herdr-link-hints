package ansi

import (
	"reflect"
	"testing"
)

func TestParseLinks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		frame string
		want  []Link
	}{
		{
			name:  "absolute cursor placement",
			frame: "\x1b[2J\x1b[1;1Hsee \x1b]8;;https://github.com/o/r/pull/232\x1b\\#232\x1b]8;;\x1b\\ and https://a.io/plain",
			want:  []Link{{URL: "https://github.com/o/r/pull/232", Row: 0, Col: 4, Label: "#232"}},
		},
		{
			name:  "BEL terminator and CUP",
			frame: "\x1b[12;7H\x1b]8;;https://e.io/t\x07HI\x1b]8;;\x07 tail",
			want:  []Link{{URL: "https://e.io/t", Row: 11, Col: 6, Label: "HI"}},
		},
		{
			name:  "cursor forward",
			frame: "\x1b[3;1HAB\x1b[2C\x1b]8;;https://e.io/y\x1b\\Z\x1b]8;;\x1b\\",
			want:  []Link{{URL: "https://e.io/y", Row: 2, Col: 4, Label: "Z"}},
		},
		{
			name:  "zero parameter moves one cell",
			frame: "\x1b[1;1HA\x1b[0C\x1b]8;;https://e.io/z\x1b\\Q\x1b]8;;\x1b\\",
			want:  []Link{{URL: "https://e.io/z", Row: 0, Col: 2, Label: "Q"}},
		},
		{
			name:  "newline advances the row",
			frame: "\x1b[1;1Hone\ntwo \x1b]8;;https://e.io/n\x1b\\N\x1b]8;;\x1b\\",
			want:  []Link{{URL: "https://e.io/n", Row: 1, Col: 4, Label: "N"}},
		},
		{
			name:  "empty open is not a link",
			frame: "\x1b[1;1H\x1b]8;;\x1b\\plain",
		},
		{
			name:  "unterminated link is dropped",
			frame: "\x1b[5;5H\x1b]8;;https://e.io/x\x1b\\half",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseLinks([]byte(tc.frame)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseLinks() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseLinksLabelIsCapped(t *testing.T) {
	t.Parallel()
	long := make([]byte, 0, 512)
	for range 400 {
		long = append(long, 'x')
	}
	frame := "\x1b[1;1H\x1b]8;;https://e.io/long\x1b\\" + string(long) + "\x1b]8;;\x1b\\"
	got := ParseLinks([]byte(frame))
	if len(got) != 1 {
		t.Fatalf("ParseLinks() = %+v, want one link", got)
	}
	if len(got[0].Label) != maxLabelRunes {
		t.Fatalf("label length = %d, want %d", len(got[0].Label), maxLabelRunes)
	}
}

func TestParseLinksHugeParameterFallsBack(t *testing.T) {
	t.Parallel()
	frame := "\x1b[99999999999999999999;1H\x1b]8;;https://e.io/x\x1b\\A\x1b]8;;\x1b\\"
	got := ParseLinks([]byte(frame))
	if len(got) != 1 || got[0].Row != 0 {
		t.Fatalf("ParseLinks() = %+v, want a link at row 0", got)
	}
}
