//go:build darwin

package sandbox

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The host terminal is Darwin's: its request numbers and struct termios
// differ from Linux's in nearly every bit (flag positions, the c_cc indices,
// the "disabled" char, field widths, Linux's speed codes in c_cflag vs
// Darwin's numeric speeds), so they are translated here.

// Darwin requests x/sys does not name.
const (
	dwFIONREAD = 0x4004667f
	dwFREAD    = 1
	dwFWRITE   = 2
)

// ttyLibc is the libSystem terminal API reached through goffi: these take
// buffers x/sys has no wrapper for.
var (
	ttyLibcOnce sync.Once
	ttyLibcErr  error
	ttTtynameR  *ffiCall // ttyname_r
	ttPtsnameR  *ffiCall // ptsname_r
	ttGrantpt   *ffiCall // grantpt
	ttUnlockpt  *ffiCall // unlockpt
)

func initTTYLibc() error {
	ttyLibcOnce.Do(func() {
		lib, err := ffiOpenLibSystem()
		if err != nil {
			ttyLibcErr = err
			return
		}
		if ttTtynameR, ttyLibcErr = ffiPrep(lib, "ttyname_r", ffiS32, ffiS32, ffiPtr, ffiU64); ttyLibcErr != nil {
			return
		}
		if ttPtsnameR, ttyLibcErr = ffiPrep(lib, "ptsname_r", ffiS32, ffiS32, ffiPtr, ffiU64); ttyLibcErr != nil {
			return
		}
		if ttGrantpt, ttyLibcErr = ffiPrep(lib, "grantpt", ffiS32, ffiS32); ttyLibcErr != nil {
			return
		}
		ttUnlockpt, ttyLibcErr = ffiPrep(lib, "unlockpt", ffiS32, ffiS32)
	})
	return ttyLibcErr
}

func libcTtynameR(fd int32, buf *byte, n uint64) int32 {
	var r int32
	ttTtynameR.call(unsafe.Pointer(&r), unsafe.Pointer(&fd), unsafe.Pointer(&buf), unsafe.Pointer(&n))
	return r
}

func libcPtsnameR(fd int32, buf *byte, n uint64) int32 {
	var r int32
	ttPtsnameR.call(unsafe.Pointer(&r), unsafe.Pointer(&fd), unsafe.Pointer(&buf), unsafe.Pointer(&n))
	return r
}

func libcGrantpt(fd int32) int32 {
	var r int32
	ttGrantpt.call(unsafe.Pointer(&r), unsafe.Pointer(&fd))
	return r
}

func libcUnlockpt(fd int32) int32 {
	var r int32
	ttUnlockpt.call(unsafe.Pointer(&r), unsafe.Pointer(&fd))
	return r
}

// bitMap pairs a Linux flag bit (or multi-bit field value) with Darwin's.
type bitMap struct{ lx, dw uint64 }

var iflagMap = []bitMap{
	{0x1, unix.IGNBRK}, {0x2, unix.BRKINT}, {0x4, unix.IGNPAR}, {0x8, unix.PARMRK},
	{0x10, unix.INPCK}, {0x20, unix.ISTRIP}, {0x40, unix.INLCR}, {0x80, unix.IGNCR},
	{0x100, unix.ICRNL}, {0x400, unix.IXON}, {0x800, unix.IXANY}, {0x1000, unix.IXOFF},
	{0x2000, unix.IMAXBEL}, {0x4000, unix.IUTF8},
}

var oflagMap = []bitMap{
	{0x1, unix.OPOST}, {0x4, unix.ONLCR}, {0x8, unix.OCRNL}, {0x10, unix.ONOCR},
	{0x20, unix.ONLRET}, {0x40, unix.OFILL}, {0x80, unix.OFDEL},
	{0x100, unix.NL1}, {0x2000, unix.BS1}, {0x4000, unix.VT1}, {0x8000, unix.FF1},
}

// Multi-bit oflag delay fields: Linux mask, Darwin mask, then value pairs.
var oflagFields = []struct {
	lxMask, dwMask uint64
	vals           []bitMap
}{
	{0x600, unix.CRDLY, []bitMap{{0x200, unix.CR1}, {0x400, unix.CR2}, {0x600, unix.CR3}}},
	{0x1800, unix.TABDLY, []bitMap{{0x800, unix.TAB1}, {0x1000, unix.TAB2}, {0x1800, unix.TAB3}}},
}

var cflagMap = []bitMap{
	{0x40, unix.CSTOPB}, {0x80, unix.CREAD}, {0x100, unix.PARENB}, {0x200, unix.PARODD},
	{0x400, unix.HUPCL}, {0x800, unix.CLOCAL}, {0x80000000, unix.CRTSCTS},
}

var lflagMap = []bitMap{
	{0x1, unix.ISIG}, {0x2, unix.ICANON}, {0x8, unix.ECHO}, {0x10, unix.ECHOE},
	{0x20, unix.ECHOK}, {0x40, unix.ECHONL}, {0x80, unix.NOFLSH}, {0x100, unix.TOSTOP},
	{0x200, unix.ECHOCTL}, {0x400, unix.ECHOPRT}, {0x800, unix.ECHOKE}, {0x1000, unix.FLUSHO},
	{0x4000, unix.PENDIN}, {0x8000, unix.IEXTEN}, {0x10000, unix.EXTPROC},
}

// ccMap pairs Linux c_cc indices with Darwin's.
var ccMap = []struct{ lx, dw int }{
	{0, unix.VINTR}, {1, unix.VQUIT}, {2, unix.VERASE}, {3, unix.VKILL},
	{4, unix.VEOF}, {5, unix.VTIME}, {6, unix.VMIN}, {8, unix.VSTART},
	{9, unix.VSTOP}, {10, unix.VSUSP}, {11, unix.VEOL}, {12, unix.VREPRINT},
	{13, unix.VDISCARD}, {14, unix.VWERASE}, {15, unix.VLNEXT}, {16, unix.VEOL2},
}

// dwVDISABL is Darwin _POSIX_VDISABLE; Linux disables a char with 0.
const dwVDISABL = 0xff

func mapTo(v uint64, m []bitMap, toLx bool) uint64 {
	var out uint64
	for _, b := range m {
		from, to := b.dw, b.lx
		if !toLx {
			from, to = b.lx, b.dw
		}
		if v&from == from {
			out |= to
		}
	}
	return out
}

func mapMask(m []bitMap, lx bool) uint64 {
	var out uint64
	for _, b := range m {
		if lx {
			out |= b.lx
		} else {
			out |= b.dw
		}
	}
	return out
}

func toLxTermios(d *unix.Termios) *lxTermios {
	t := &lxTermios{
		iflag: uint32(mapTo(d.Iflag, iflagMap, true)),
		oflag: uint32(mapTo(d.Oflag, oflagMap, true)),
		cflag: uint32(mapTo(d.Cflag, cflagMap, true) | (d.Cflag&unix.CSIZE)>>4),
		// Darwin raises PENDIN itself on every switch to canonical mode
		// (Linux never does), so it would read back as a flag nobody set.
		lflag: uint32(mapTo(d.Lflag&^unix.PENDIN, lflagMap, true)),
	}
	for _, f := range oflagFields {
		for _, v := range f.vals {
			if d.Oflag&f.dwMask == v.dw {
				t.oflag |= uint32(v.lx)
			}
		}
	}
	for _, c := range ccMap {
		v := d.Cc[c.dw]
		if v == dwVDISABL && c.dw != unix.VMIN && c.dw != unix.VTIME {
			v = 0
		}
		t.cc[c.lx] = v
	}
	ocode, _ := baudCode(d.Ospeed)
	t.cflag |= uint32(ocode)
	if d.Ispeed != d.Ospeed {
		icode, _ := baudCode(d.Ispeed)
		t.cflag |= uint32(icode) << 16
	}
	t.ispeed, t.ospeed = uint32(d.Ispeed), uint32(d.Ospeed)
	return t
}

// applyLxTermios folds a Linux termios into the host's current settings,
// keeping the Darwin-only bits and chars (VSTATUS, NOKERNINFO, ...) as they are.
func applyLxTermios(d *unix.Termios, t *lxTermios, two bool) {
	keep := func(cur, mask uint64) uint64 { return cur &^ mask }
	d.Iflag = keep(d.Iflag, mapMask(iflagMap, false)) | mapTo(uint64(t.iflag), iflagMap, false)
	om := mapMask(oflagMap, false)
	for _, f := range oflagFields {
		om |= f.dwMask
	}
	d.Oflag = keep(d.Oflag, om) | mapTo(uint64(t.oflag), oflagMap, false)
	for _, f := range oflagFields {
		for _, v := range f.vals {
			if uint64(t.oflag)&f.lxMask == v.lx {
				d.Oflag |= v.dw
			}
		}
	}
	d.Cflag = keep(d.Cflag, mapMask(cflagMap, false)|unix.CSIZE) |
		mapTo(uint64(t.cflag), cflagMap, false) | (uint64(t.cflag)&lxCSIZE)<<4
	d.Lflag = keep(d.Lflag, mapMask(lflagMap, false)) | mapTo(uint64(t.lflag), lflagMap, false)
	for _, c := range ccMap {
		v := t.cc[c.lx]
		if v == 0 && c.dw != unix.VMIN && c.dw != unix.VTIME {
			v = dwVDISABL
		}
		d.Cc[c.dw] = v
	}
	ocode := uint64(t.cflag) & lxCBAUD
	icode := (uint64(t.cflag) & lxCIBAUD) >> 16
	ospeed := codeBaud(ocode)
	if two && ocode == lxBOTHER {
		ospeed = uint64(t.ospeed)
	}
	ispeed := ospeed
	if icode != 0 {
		ispeed = codeBaud(icode)
		if two && icode == lxBOTHER {
			ispeed = uint64(t.ispeed)
		}
	}
	d.Ospeed, d.Ispeed = ospeed, ispeed
}

const hostFIONREAD = dwFIONREAD

// hostVDisable is the c_cc value that disables a control char.
const hostVDisable = dwVDISABL

// hostSetTermios applies cur with the Darwin request matching Linux's req
// (TCSETS, TCSETSW, TCSETSF and their termios2 forms).
func hostSetTermios(fd int, req uint64, cur *unix.Termios) error {
	dreq := map[uint64]uint{
		lxTCSETS: unix.TIOCSETA, lxTCSETSW: unix.TIOCSETAW, lxTCSETSF: unix.TIOCSETAF,
		lxTCSETS2: unix.TIOCSETA, lxTCSETSW2: unix.TIOCSETAW, lxTCSETSF2: unix.TIOCSETAF,
	}[req]
	return unix.IoctlSetTermios(fd, dreq, cur)
}

// hostFlush discards input (0), output (1) or both (2).
func hostFlush(fd, which int) error {
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, []int{dwFREAD, dwFWRITE, dwFREAD | dwFWRITE}[which])
}

// hostOutput suspends or resumes output.
func hostOutput(fd int, on bool) error {
	if on {
		return unix.IoctlSetInt(fd, unix.TIOCSTART, 0)
	}
	return unix.IoctlSetInt(fd, unix.TIOCSTOP, 0)
}

func hostDrain(fd int) error { return unix.IoctlSetInt(fd, unix.TIOCDRAIN, 0) }
