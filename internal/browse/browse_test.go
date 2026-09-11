package browse

import "testing"

func TestOpenable(t *testing.T) {
	t.Parallel()
	for _, url := range []string{"https://x.io", "HTTP://x.io", "mailto:a@b.io", "file:///tmp/x"} {
		if !openable(url) {
			t.Errorf("openable(%q) = false", url)
		}
	}
	for _, url := range []string{"", "x.io", "javascript:alert(1)", "-nope", "data:text/html,x"} {
		if openable(url) {
			t.Errorf("openable(%q) = true", url)
		}
	}
}

func TestOpenRejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()
	if err := Open("javascript:alert(1)"); err == nil {
		t.Fatal("Open should refuse a scheme outside the allow list")
	}
}
