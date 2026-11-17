package sandbox

import "unicode/utf8"

// ldisc is a Linux terminal's line discipline (n_tty) for a terminal the
// supervisor implements itself: input processing (ICRNL and friends),
// canonical line editing, echo, the signal characters, and output processing
// (OPOST, ONLCR, tab expansion). It is pure state; the host's terminal object
// locks it, feeds it input and drains its output, and raises the signals it
// reports. Host-neutral and free of host calls, so it is tested on Linux.
type ldisc struct {
	t  lxTermios
	ws [4]uint16 // struct winsize: rows, cols, xpixel, ypixel

	line  []byte // canonical: the line being edited
	ready []byte // what a read may take
	// bounds are the canonical line ends in ready, as offsets; a read stops at
	// the first. An end at offset 0 is a VEOF on an empty line: a read of 0.
	bounds []int

	out     []byte // processed output and echo, not yet written out
	col     int    // output column, for tabs, ONOCR and erasing
	lineCol int    // the column where the line being edited started
	lnext   bool   // the next input char is literal (VLNEXT)
	stopped bool   // output stopped by VSTOP (IXON)
}

// The signals the line discipline raises.
const (
	ldSigINT  = 2
	ldSigQUIT = 3
	ldSigTSTP = 20
)

// ldiscBufMax bounds the input a terminal holds (Linux's N_TTY_BUF_SIZE).
const ldiscBufMax = 4095

func newLdisc() *ldisc {
	return &ldisc{t: stdTermios()}
}

func (l *ldisc) canon() bool { return l.t.lflag&lxICANON != 0 }

func (l *ldisc) cc(i int) byte { return l.t.cc[i] }

// is reports whether c is the enabled special char at c_cc index i (0 disables).
func (l *ldisc) is(c byte, i int) bool { return l.t.cc[i] != 0 && c == l.t.cc[i] }

// setTermios installs new settings. Leaving canonical mode makes the line
// being edited readable; entering it turns what is pending into a line.
func (l *ldisc) setTermios(n lxTermios) {
	was := l.canon()
	l.t = n
	switch now := l.canon(); {
	case was && !now:
		l.ready = append(l.ready, l.line...)
		l.line, l.bounds = nil, nil
	case !was && now && len(l.ready) > 0:
		l.bounds = []int{len(l.ready)}
	}
	if l.t.iflag&lxIXON == 0 {
		l.stopped = false
	}
}

// readable reports whether a read would return now (data, or a canonical EOF).
func (l *ldisc) readable() bool {
	if l.canon() {
		return len(l.bounds) > 0
	}
	return len(l.ready) > 0
}

// avail is FIONREAD's count.
func (l *ldisc) avail() int {
	if l.canon() {
		if len(l.bounds) == 0 {
			return 0
		}
		return l.bounds[len(l.bounds)-1]
	}
	return len(l.ready)
}

// take removes up to n readable bytes: in canonical mode no further than the
// end of the first line. eof reports a canonical VEOF on an empty line.
func (l *ldisc) take(n int) (b []byte, eof bool) {
	if l.canon() {
		if len(l.bounds) == 0 {
			return nil, false
		}
		end := l.bounds[0]
		if end == 0 {
			l.bounds = l.bounds[1:]
			return nil, true
		}
		n = min(n, end)
	} else {
		n = min(n, len(l.ready))
	}
	b = append([]byte(nil), l.ready[:n]...)
	l.ready = l.ready[n:]
	if len(l.ready) == 0 {
		l.ready = nil
	}
	for i := range l.bounds {
		l.bounds[i] -= n
	}
	if len(l.bounds) > 0 && l.bounds[0] == 0 && n > 0 {
		l.bounds = l.bounds[1:]
	}
	return b, false
}

// flushIn drops pending input (TCIFLUSH, a signal char without NOFLSH).
func (l *ldisc) flushIn() {
	l.line, l.ready, l.bounds = nil, nil, nil
	l.lnext = false
}

// takeOut removes up to n bytes of pending output.
func (l *ldisc) takeOut(n int) []byte {
	if l.stopped {
		return nil
	}
	n = min(n, len(l.out))
	b := append([]byte(nil), l.out[:n]...)
	l.out = l.out[n:]
	if len(l.out) == 0 {
		l.out = nil
	}
	return b
}

// room is how much more input the terminal takes.
func (l *ldisc) room() int {
	return max(ldiscBufMax-len(l.ready)-len(l.line), 0)
}

// receive processes input as typed at the terminal (written to a pty master,
// or read from the console) and returns the signals it raises for the
// foreground process group, in order. Output it causes (echo) is appended to
// l.out. In canonical mode a full line drops further chars, as Linux does;
// otherwise input beyond the room is dropped by the caller first.
func (l *ldisc) receive(in []byte) (sigs []int) {
	for _, c := range in {
		if s := l.receiveByte(c); s != 0 {
			sigs = append(sigs, s)
		}
	}
	return sigs
}

func (l *ldisc) receiveByte(c byte) int {
	t := &l.t
	if t.iflag&lxISTRIP != 0 {
		c &= 0x7f
	}
	if t.iflag&lxIUCLC != 0 && t.lflag&lxIEXTEN != 0 && c >= 'A' && c <= 'Z' {
		c += 'a' - 'A'
	}
	if l.lnext {
		l.lnext = false
		l.store(c)
		return 0
	}
	if t.iflag&lxIXON != 0 {
		switch {
		case l.is(c, lxVSTOP):
			l.stopped = true
			return 0
		case l.is(c, lxVSTART):
			l.stopped = false
			return 0
		case l.stopped && t.iflag&lxIXANY != 0:
			l.stopped = false
		}
	}
	if t.lflag&lxISIG != 0 {
		sig := 0
		switch {
		case l.is(c, lxVINTR):
			sig = ldSigINT
		case l.is(c, lxVQUIT):
			sig = ldSigQUIT
		case l.is(c, lxVSUSP):
			sig = ldSigTSTP
		}
		if sig != 0 {
			if t.lflag&lxNOFLSH == 0 {
				l.flushIn()
				l.out = nil
			}
			l.stopped = false
			if t.lflag&lxECHO != 0 {
				l.echoChar(c)
				l.lineCol = l.col
			}
			return sig
		}
	}
	switch {
	case c == '\r' && t.iflag&lxIGNCR != 0:
		return 0
	case c == '\r' && t.iflag&lxICRNL != 0:
		c = '\n'
	case c == '\n' && t.iflag&lxINLCR != 0:
		c = '\r'
	}
	if !l.canon() {
		if len(l.ready) >= ldiscBufMax {
			return 0
		}
		if t.lflag&lxECHO != 0 {
			l.echoChar(c)
		}
		l.ready = append(l.ready, c)
		return 0
	}
	ext := t.lflag&lxIEXTEN != 0
	switch {
	case l.is(c, lxVERASE), ext && l.is(c, lxVWERASE), l.is(c, lxVKILL):
		l.erase(c)
		return 0
	case ext && l.is(c, lxVLNEXT):
		l.lnext = true
		if t.lflag&lxECHO != 0 && t.lflag&lxECHOCTL != 0 {
			l.output([]byte("^\b"))
		}
		return 0
	case ext && l.is(c, lxVREPRINT):
		if t.lflag&lxECHO != 0 {
			l.echoChar(c)
			l.output([]byte{'\n'})
			l.lineCol = l.col
			for _, b := range l.line {
				l.echoChar(b)
			}
		}
		return 0
	case l.is(c, lxVEOF):
		l.endLine()
		return 0
	case c == '\n', l.is(c, lxVEOL), ext && l.is(c, lxVEOL2):
		if t.lflag&lxECHO != 0 || c == '\n' && t.lflag&lxECHONL != 0 {
			l.echoChar(c)
		}
		l.line = append(l.line, c)
		l.endLine()
		return 0
	}
	l.store(c)
	return 0
}

// store adds a plain char to the input, with its echo.
func (l *ldisc) store(c byte) {
	if l.canon() {
		if len(l.line)+len(l.ready) >= ldiscBufMax-1 {
			if l.t.iflag&lxIMAXBEL != 0 {
				l.output([]byte{7})
			}
			return
		}
		l.line = append(l.line, c)
	} else {
		if len(l.ready) >= ldiscBufMax {
			return
		}
		l.ready = append(l.ready, c)
	}
	if l.t.lflag&lxECHO != 0 {
		l.echoChar(c)
	}
}

// endLine makes the line being edited readable (its terminator stored, or
// none for VEOF).
func (l *ldisc) endLine() {
	l.ready = append(l.ready, l.line...)
	l.bounds = append(l.bounds, len(l.ready))
	l.line = nil
	l.lineCol = l.col
}

func isCtl(c byte) bool { return c < 0x20 || c == 0x7f }

// echoChar echoes one input char: control chars as ^X under ECHOCTL (but not
// tab or newline, and not the START/STOP chars), otherwise as is.
func (l *ldisc) echoChar(c byte) {
	if l.t.lflag&lxECHOCTL != 0 && isCtl(c) && c != '\t' && c != '\n' && !l.is(c, lxVSTART) && !l.is(c, lxVSTOP) {
		l.output([]byte{'^', c ^ 0x40})
		return
	}
	l.output([]byte{c})
}

// width is the columns the echo of c took at column col.
func (l *ldisc) width(c byte, col int) int {
	switch {
	case c == '\t':
		return 8 - col%8
	case isCtl(c):
		if l.t.lflag&lxECHOCTL != 0 {
			return 2
		}
		return 0
	case l.t.iflag&lxIUTF8 != 0 && c&0xc0 == 0x80:
		return 0
	}
	return 1
}

// erase serves VERASE, VWERASE and VKILL on the line being edited.
func (l *ldisc) erase(c byte) {
	t := &l.t
	kill := l.is(c, lxVKILL)
	word := !kill && t.lflag&lxIEXTEN != 0 && l.is(c, lxVWERASE)
	if len(l.line) == 0 {
		return
	}
	echo := t.lflag&lxECHO != 0
	if kill && (!echo || t.lflag&lxECHOK == 0 || t.lflag&lxECHOKE == 0 || t.lflag&lxECHOE == 0) {
		l.line = nil
		if echo {
			l.echoChar(c)
			if t.lflag&lxECHOK != 0 {
				l.output([]byte{'\n'})
				l.lineCol = l.col
			}
		}
		return
	}
	n := len(l.line)
	switch {
	case kill:
		n = 0
	case word:
		for n > 0 && (l.line[n-1] == ' ' || l.line[n-1] == '\t') {
			n--
		}
		for n > 0 && l.line[n-1] != ' ' && l.line[n-1] != '\t' {
			n--
		}
	default:
		n--
		if t.iflag&lxIUTF8 != 0 {
			for n > 0 && l.line[n]&0xc0 == 0x80 {
				n--
			}
		}
	}
	gone := l.line[n:]
	l.line = l.line[:n]
	if !echo {
		return
	}
	if !kill && !word && t.lflag&lxECHOE == 0 {
		l.echoChar(c)
		return
	}
	// Back over the columns the erased chars took, measured from where the
	// line started.
	col := l.lineCol
	for _, b := range l.line {
		col += l.width(b, col)
	}
	end := col
	for _, b := range gone {
		end += l.width(b, end)
	}
	for i := 0; i < end-col; i++ {
		if t.lflag&lxECHOE != 0 {
			l.output([]byte("\b \b"))
		} else {
			l.output([]byte{'\b'})
		}
	}
}

// output appends b, written to the terminal or echoed, to l.out after OPOST
// processing.
func (l *ldisc) output(b []byte) {
	t := &l.t
	if t.oflag&lxOPOST == 0 {
		l.out = append(l.out, b...)
		for _, c := range b {
			l.track(c)
		}
		return
	}
	for _, c := range b {
		switch c {
		case '\n':
			if t.oflag&lxONLRET != 0 {
				l.col = 0
			}
			if t.oflag&lxONLCR != 0 {
				l.out = append(l.out, '\r', '\n')
				l.col = 0
				continue
			}
		case '\r':
			if t.oflag&lxONOCR != 0 && l.col == 0 {
				continue
			}
			if t.oflag&lxOCRNL != 0 {
				c = '\n'
				if t.oflag&lxONLRET != 0 {
					l.col = 0
				}
				l.out = append(l.out, c)
				continue
			}
			l.col = 0
		case '\t':
			sp := 8 - l.col%8
			l.col += sp
			if t.oflag&lxTABDLY == lxXTABS {
				for range sp {
					l.out = append(l.out, ' ')
				}
				continue
			}
		case '\b':
			if l.col > 0 {
				l.col--
			}
		default:
			if t.oflag&lxOLCUC != 0 && c >= 'a' && c <= 'z' {
				c -= 'a' - 'A'
			}
			if c >= 0x20 && c != 0x7f && utf8.RuneStart(c) {
				l.col++
			}
		}
		l.out = append(l.out, c)
	}
}

// track follows the column through raw output.
func (l *ldisc) track(c byte) {
	switch {
	case c == '\n', c == '\r':
		l.col = 0
	case c == '\t':
		l.col += 8 - l.col%8
	case c == '\b':
		if l.col > 0 {
			l.col--
		}
	case c >= 0x20 && c != 0x7f && utf8.RuneStart(c):
		l.col++
	}
}
