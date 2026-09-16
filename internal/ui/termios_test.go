package ui

import (
	"syscall"
	"testing"
)

// keys.go maps ctrl-c and ctrl-d from the bytes raw mode delivers, so a
// raw mode that left ISIG on would turn them back into signals and take
// the picker's own quit path away.
func TestRawModeClearsTheFlagsKeysDependOn(t *testing.T) {
	t.Parallel()
	// The flag fields are uint32 on linux and uint64 on darwin, so every
	// value here is derived rather than written with a width.
	var cooked syscall.Termios
	cooked.Iflag = ^cooked.Iflag
	cooked.Oflag = ^cooked.Oflag
	cooked.Lflag = ^cooked.Lflag
	cooked.Cflag = ^cooked.Cflag

	raw := rawMode(cooked)

	// Compared as bools, because the flag fields are uint32 on linux and
	// uint64 on darwin and a width written here would only fit one.
	cleared := func(name string, set bool) {
		if set {
			t.Errorf("%s still set in raw mode", name)
		}
	}
	cleared("ECHO", raw.Lflag&syscall.ECHO != 0)
	cleared("ICANON", raw.Lflag&syscall.ICANON != 0)
	cleared("ISIG", raw.Lflag&syscall.ISIG != 0)
	cleared("IEXTEN", raw.Lflag&syscall.IEXTEN != 0)
	cleared("ICRNL", raw.Iflag&syscall.ICRNL != 0)
	cleared("IXON", raw.Iflag&syscall.IXON != 0)
	cleared("OPOST", raw.Oflag&syscall.OPOST != 0)

	if raw.Cflag&syscall.CS8 == 0 {
		t.Error("CS8 not set: a hint code byte could be stripped to 7 bits")
	}
	if raw.Cc[syscall.VMIN] != 1 || raw.Cc[syscall.VTIME] != 0 {
		t.Errorf("VMIN/VTIME = %d/%d, want 1/0 so a read returns on one byte", raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME])
	}
}
