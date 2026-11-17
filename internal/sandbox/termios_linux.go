package sandbox

import "golang.org/x/sys/unix"

// The host terminal is Linux's: its requests and struct termios are the
// guest's own.

const hostFIONREAD = unix.TIOCINQ

// hostVDisable is the c_cc value that disables a control char.
const hostVDisable = 0

func toLxTermios(d *unix.Termios) *lxTermios {
	t := &lxTermios{iflag: d.Iflag, oflag: d.Oflag, cflag: d.Cflag, lflag: d.Lflag, line: d.Line,
		ispeed: d.Ispeed, ospeed: d.Ospeed}
	copy(t.cc[:], d.Cc[:])
	return t
}

// applyLxTermios sets d to t; the speeds only from a termios2.
func applyLxTermios(d *unix.Termios, t *lxTermios, two bool) {
	d.Iflag, d.Oflag, d.Cflag, d.Lflag, d.Line = t.iflag, t.oflag, t.cflag, t.lflag, t.line
	copy(d.Cc[:], t.cc[:])
	if two {
		d.Ispeed, d.Ospeed = t.ispeed, t.ospeed
	}
}

// hostSetTermios applies cur with Linux's req (TCSETS, TCSETSW, TCSETSF and
// their termios2 forms, which the host ioctl takes as the plain ones).
func hostSetTermios(fd int, req uint64, cur *unix.Termios) error {
	r := map[uint64]uint{
		lxTCSETS: unix.TCSETS, lxTCSETSW: unix.TCSETSW, lxTCSETSF: unix.TCSETSF,
		lxTCSETS2: unix.TCSETS, lxTCSETSW2: unix.TCSETSW, lxTCSETSF2: unix.TCSETSF,
	}[req]
	return unix.IoctlSetTermios(fd, r, cur)
}

// hostFlush discards input (0), output (1) or both (2).
func hostFlush(fd, which int) error { return unix.IoctlSetInt(fd, unix.TCFLSH, which) }

// hostOutput suspends or resumes output.
func hostOutput(fd int, on bool) error {
	if on {
		return unix.IoctlSetInt(fd, unix.TCXONC, unix.TCOON)
	}
	return unix.IoctlSetInt(fd, unix.TCXONC, unix.TCOOFF)
}

func hostDrain(fd int) error { return unix.IoctlSetInt(fd, unix.TCSBRK, 1) }
