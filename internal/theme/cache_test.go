package theme

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Not parallel: this points HOME at a temp dir, so callers must stay
// sequential.
func withTempCache(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserCacheDir consults XDG_CACHE_HOME only on some platforms,
	// so point HOME at the temp dir instead: on darwin the cache lands
	// under $HOME/Library/Caches.
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return home
}

func fullColors() Colors {
	return Colors{
		Foreground: color.RGBA{R: 0xCD, G: 0xD6, B: 0xF4, A: 0xFF},
		Background: color.RGBA{R: 0x1E, G: 0x1E, B: 0x2E, A: 0xFF},
		Accent:     color.RGBA{R: 0xF9, G: 0xE2, B: 0xAF, A: 0xFF},
	}
}

func TestCacheRoundTrip(t *testing.T) {
	withTempCache(t)
	want := fullColors()
	if err := Save("iTerm.app", want); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	got, ok := Load("iTerm.app")
	if !ok {
		t.Fatal("Load() missed a reply that was just saved")
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestCacheEmptyProgramIsAMiss(t *testing.T) {
	withTempCache(t)
	if _, ok := Load(""); ok {
		t.Fatal("Load(\"\") hit")
	}
	if err := Save("", fullColors()); err != nil {
		t.Fatalf("Save(\"\") = %v, want no error for the no-op", err)
	}
}

func TestCacheCorruptFileIsAMiss(t *testing.T) {
	withTempCache(t)
	path, ok := cachePath("vscode")
	if !ok {
		t.Fatal("cachePath() refused a plain program name")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load("vscode"); ok {
		t.Fatal("Load() hit on a corrupt file")
	}
}

func TestCacheRefusesNonOpaqueColours(t *testing.T) {
	withTempCache(t)
	if err := Save("ghostty", Colors{}); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	if _, ok := Load("ghostty"); ok {
		t.Fatal("Load() hit on colours that were never opaque")
	}
}

func TestCacheStaleFileIsAMiss(t *testing.T) {
	withTempCache(t)
	if err := Save("iTerm.app", fullColors()); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	path, ok := cachePath("iTerm.app")
	if !ok {
		t.Fatal("cachePath() refused a plain program name")
	}
	old := time.Now().Add(-2 * cacheTTL)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load("iTerm.app"); ok {
		t.Fatal("Load() hit on a stale file")
	}
}

func TestCacheDisabledSkipsReadAndWrite(t *testing.T) {
	withTempCache(t)
	if err := Save("iTerm.app", fullColors()); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	t.Setenv("HINTS_NO_THEME_CACHE", "1")
	if _, ok := Load("iTerm.app"); ok {
		t.Fatal("Load() hit with HINTS_NO_THEME_CACHE set")
	}
	if err := Save("ghostty", fullColors()); err != nil {
		t.Fatalf("Save() = %v, want no error for the no-op", err)
	}
	path, ok := cachePath("ghostty")
	if !ok {
		t.Fatal("cachePath() refused a plain program name")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Save() wrote a file with HINTS_NO_THEME_CACHE set")
	}
}

func TestCacheKeyCannotEscapeTheCacheDir(t *testing.T) {
	home := withTempCache(t)
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"../../etc/passwd", "/abs", "a/b\\c"} {
		path, ok := cachePath(program)
		if !ok {
			continue
		}
		if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
			t.Fatalf("cachePath(%q) = %q escapes %q", program, path, dir)
		}
		if err := Save(program, fullColors()); err != nil {
			t.Fatalf("Save(%q) = %v", program, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "herdr-link-hints"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no cache file was written inside the cache dir")
	}
	escaped := filepath.Join(home, "etc")
	if _, err := os.Stat(escaped); !os.IsNotExist(err) {
		t.Fatalf("%q exists outside the cache dir", escaped)
	}
}

func TestSanitize(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"iTerm.app", "iTerm.app"},
		{"vscode", "vscode"},
		{"", ""},
		{"../../etc", "etc"},
		{"a b", "a_b"},
	}
	for _, tc := range cases {
		if got := sanitize(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := sanitize(strings.Repeat("a", 100)); len(got) != 64 {
		t.Errorf("sanitize() kept %d chars, want 64", len(got))
	}
}
