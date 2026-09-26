package app

// A renderer snapshot cannot retain the decoder's unfinished UTF-8 character or
// a partly parsed escape sequence. Track those small states without retaining
// terminal output. Only a complete parser boundary may become a handoff barrier.
// The rules follow the bundled xterm UTF-8 decoder and VT parser conservatively:
// malformed CSI/DCS input can postpone a handoff, but cannot release half a string.
type terminalBoundary struct {
	state uint8
	left  int
	rune  rune
	min   rune
}

const (
	boundaryGround uint8 = iota
	boundaryEscape
	boundaryIntermediate
	boundaryCSI
	boundaryDCS
	boundaryOSC
	boundaryString
)

func (b *terminalBoundary) safe() bool { return b.left == 0 && b.state == boundaryGround }

func (b *terminalBoundary) write(data []byte) {
	for _, c := range data {
		if b.left > 0 {
			if c&0xc0 == 0x80 {
				b.rune = b.rune<<6 | rune(c&0x3f)
				b.left--
				if b.left == 0 && b.rune >= b.min && b.rune <= 0x10ffff && (b.rune < 0xd800 || b.rune > 0xdfff) {
					b.codepoint(b.rune)
				}
				continue
			}
			// xterm discards incomplete bytes when the continuation is invalid,
			// then processes the current byte again as a new character.
			b.left = 0
		}
		switch {
		case c < 0x80:
			b.codepoint(rune(c))
		case c&0xe0 == 0xc0:
			b.left, b.rune, b.min = 1, rune(c&0x1f), 0x80
		case c&0xf0 == 0xe0:
			b.left, b.rune, b.min = 2, rune(c&0x0f), 0x800
		case c&0xf8 == 0xf0:
			b.left, b.rune, b.min = 3, rune(c&0x07), 0x10000
		}
	}
}

func (b *terminalBoundary) codepoint(c rune) {
	// VT anywhere transitions, including UTF-8 encoded C1 controls. Raw invalid
	// continuation bytes never reach this function (xterm ignores those bytes).
	switch c {
	case 0x1b:
		b.state = boundaryEscape
		return
	case 0x18, 0x1a, 0x9c:
		b.state = boundaryGround
		return
	case 0x90:
		b.state = boundaryDCS
		return
	case 0x9b:
		b.state = boundaryCSI
		return
	case 0x9d:
		b.state = boundaryOSC
		return
	case 0x98, 0x9e, 0x9f:
		b.state = boundaryString
		return
	}
	if c >= 0x80 && c <= 0x9a {
		b.state = boundaryGround
		return
	}
	switch b.state {
	case boundaryEscape:
		switch c {
		case '[':
			b.state = boundaryCSI
		case ']':
			b.state = boundaryOSC
		case 'P':
			b.state = boundaryDCS
		case 'X', '^', '_':
			b.state = boundaryString
		default:
			if c >= 0x20 && c <= 0x2f {
				b.state = boundaryIntermediate
			} else if c >= 0x30 && c <= 0x7e {
				b.state = boundaryGround
			}
		}
	case boundaryIntermediate:
		if c >= 0x30 && c <= 0x7e {
			b.state = boundaryGround
		}
	case boundaryCSI:
		if c >= 0x40 && c <= 0x7e {
			b.state = boundaryGround
		}
	case boundaryOSC:
		if c == 0x07 {
			b.state = boundaryGround
		}
	}
}
