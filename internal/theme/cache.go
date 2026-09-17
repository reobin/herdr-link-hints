package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cacheTTL bounds how long a saved reply is trusted.
const cacheTTL = 24 * time.Hour

func cachingDisabled() bool {
	v := os.Getenv("HINTS_NO_THEME_CACHE")
	return v != "" && v != "0"
}

// Load returns colours from an earlier run, else a miss.
func Load(program string) (Colors, bool) {
	if cachingDisabled() {
		return Colors{}, false
	}
	path, ok := cachePath(program)
	if !ok {
		return Colors{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return Colors{}, false
	}
	if time.Since(info.ModTime()) > cacheTTL {
		return Colors{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Colors{}, false
	}
	var colors Colors
	if err := json.Unmarshal(raw, &colors); err != nil {
		return Colors{}, false
	}
	if !opaque(colors) {
		return Colors{}, false
	}
	return colors, true
}

// Save remembers a full live reply for the next run.
func Save(program string, colors Colors) error {
	if cachingDisabled() {
		return nil
	}
	path, ok := cachePath(program)
	if !ok {
		return nil
	}
	if !opaque(colors) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(colors)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "theme-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func opaque(c Colors) bool {
	return c.Foreground.A == 0xFF && c.Background.A == 0xFF && c.AccentRed.A == 0xFF &&
		c.Accent.A == 0xFF && c.AccentBlue.A == 0xFF
}

// cachePath resolves where a program's reply lives.
func cachePath(program string) (string, bool) {
	key := sanitize(program)
	if key == "" {
		return "", false
	}
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return "", false
	}
	return filepath.Join(dir, "herdr-link-hints", "theme-"+key+".json"), true
}

// sanitize keeps the key to one file-safe segment.
func sanitize(program string) string {
	var b strings.Builder
	for _, r := range program {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 64 {
			return strings.Trim(b.String(), "._-")
		}
	}
	return strings.Trim(b.String(), "._-")
}
