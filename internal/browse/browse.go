// Package browse hands a URL to the desktop's default handler.
package browse

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// javascript: and data: stay out: a terminal may render them, but they are
// not ours to launch.
var schemes = []string{"http://", "https://", "ftp://", "file://", "mailto:"}

func Open(url string) error {
	if !openable(url) {
		return fmt.Errorf("refusing to open %q: unsupported scheme", url)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "--", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("no browser opener for %s", runtime.GOOS)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %q: %w", url, err)
	}
	return nil
}

func openable(url string) bool {
	lower := strings.ToLower(url)
	for _, scheme := range schemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}
