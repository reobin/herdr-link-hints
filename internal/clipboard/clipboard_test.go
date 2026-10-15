package clipboard

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestSequence(t *testing.T) {
	t.Parallel()
	got := Sequence("https://x.io/a")
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("https://x.io/a")) + "\x07"
	if got != want {
		t.Fatalf("Sequence() = %q, want %q", got, want)
	}
}

func TestCopy(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := Copy(&buf, "https://x.io/a"); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.HasPrefix(got, "\x1b]52;c;") || !strings.HasSuffix(got, "\x07") {
		t.Fatalf("Copy() wrote %q, want an OSC 52 sequence", got)
	}
}
