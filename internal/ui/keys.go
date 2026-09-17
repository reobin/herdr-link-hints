package ui

import "time"

// escapeSequenceWait separates Esc from a sequence start.
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
			// Raw mode delivers these as bytes, not signals.
			return key{kind: keyEscape}
		default:
			if b >= ' ' && b < 0x7f {
				return key{kind: keyRune, r: rune(b)}
			}
		}
	}
}

// readEscape keeps arrows from reading as Esc.
func (t *Terminal) readEscape() key {
	b, open := t.nextByte(escapeSequenceWait)
	if !open {
		return key{kind: keyEscape}
	}
	if b == ']' {
		return t.skipOSC()
	}
	if b != '[' && b != 'O' {
		return key{kind: keyEscape}
	}
	// CSI and SS3 run until a final byte in @-~.
	for {
		b, open := t.nextByte(escapeSequenceWait)
		if !open || (b >= '@' && b <= '~') {
			return key{kind: keyUnknown}
		}
	}
}

// skipOSC swallows a late OSC reply that would read as Esc.
func (t *Terminal) skipOSC() key {
	for {
		b, open := t.nextByte(escapeSequenceWait)
		if !open {
			return key{kind: keyUnknown}
		}
		if b == '\a' {
			return key{kind: keyUnknown}
		}
		if b == 0x1b {
			next, open := t.nextByte(escapeSequenceWait)
			if !open || next == '\\' {
				return key{kind: keyUnknown}
			}
		}
	}
}
