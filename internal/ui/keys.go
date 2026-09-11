package ui

import "time"

// escapeSequenceWait separates a pressed Esc from the start of a sequence:
// the rest of a sequence follows immediately, a second keypress does not.
const escapeSequenceWait = 40 * time.Millisecond

type keyKind int

const (
	keyEnd keyKind = iota // input is over
	keyRune
	keyEnter
	keyBackspace
	keyEscape
	keyUnknown // recognised but not acted on, such as an arrow
)

type key struct {
	kind keyKind
	r    rune
}

func (t *Terminal) readKey() key {
	for {
		b, open := t.nextByte(0)
		if !open {
			return key{kind: keyEnd}
		}
		switch b {
		case 0x1b:
			return t.readEscape()
		case 0x7f, 0x08:
			return key{kind: keyBackspace}
		case '\r', '\n':
			return key{kind: keyEnter}
		case 0x03, 0x04:
			// Raw mode delivers ctrl-c and ctrl-d as bytes, not signals.
			return key{kind: keyEscape}
		default:
			if b >= ' ' && b < 0x7f {
				return key{kind: keyRune, r: rune(b)}
			}
		}
	}
}

// readEscape keeps an arrow key from reading as a bare Esc, which would
// close the picker.
func (t *Terminal) readEscape() key {
	b, open := t.nextByte(escapeSequenceWait)
	if !open {
		return key{kind: keyEscape}
	}
	if b != '[' && b != 'O' {
		return key{kind: keyEscape}
	}
	// CSI and SS3 sequences run until a final byte in the @-~ range.
	for {
		b, open := t.nextByte(escapeSequenceWait)
		if !open || (b >= '@' && b <= '~') {
			return key{kind: keyUnknown}
		}
	}
}
