package sandbox

import "encoding/binary"

// Linux's side of the terminal interface: the ioctl requests, struct termios
// and its flag bits, and the speed codes. Host-neutral (no build tag) so the
// line discipline built on it is tested on Linux too.

// Linux terminal ioctl requests (the same on x86-64 and arm64).
const (
	lxTCGETS      = 0x5401
	lxTCSETS      = 0x5402
	lxTCSETSW     = 0x5403
	lxTCSETSF     = 0x5404
	lxTCSBRK      = 0x5409
	lxTCXONC      = 0x540a
	lxTCFLSH      = 0x540b
	lxTIOCEXCL    = 0x540c
	lxTIOCNXCL    = 0x540d
	lxTIOCSCTTY   = 0x540e
	lxTIOCGPGRP   = 0x540f
	lxTIOCSPGRP   = 0x5410
	lxTIOCOUTQ    = 0x5411
	lxTIOCSTI     = 0x5412
	lxTIOCGWINSZ  = 0x5413
	lxTIOCSWINSZ  = 0x5414
	lxFIONREAD    = 0x541b
	lxTIOCNOTTY   = 0x5422
	lxTIOCSETD    = 0x5423
	lxTIOCGETD    = 0x5424
	lxTCSBRKP     = 0x5425
	lxTIOCSBRK    = 0x5427
	lxTIOCCBRK    = 0x5428
	lxTIOCGSID    = 0x5429
	lxTIOCPKT     = 0x5420
	lxTCGETS2     = 0x802c542a
	lxTCSETS2     = 0x402c542b
	lxTCSETSW2    = 0x402c542c
	lxTCSETSF2    = 0x402c542d
	lxTIOCGPTN    = 0x80045430
	lxTIOCSPTLCK  = 0x40045431
	lxTIOCGPTLCK  = 0x80045439
	lxTIOCGEXCL   = 0x80045440
	lxTIOCGPTPEER = 0x5441
)

// c_iflag, c_oflag, c_cflag and c_lflag bits the line discipline acts on.
const (
	lxIGNBRK  = 0x1
	lxISTRIP  = 0x20
	lxINLCR   = 0x40
	lxIGNCR   = 0x80
	lxICRNL   = 0x100
	lxIUCLC   = 0x200
	lxIXON    = 0x400
	lxIXANY   = 0x800
	lxIMAXBEL = 0x2000
	lxIUTF8   = 0x4000

	lxOPOST  = 0x1
	lxOLCUC  = 0x2
	lxONLCR  = 0x4
	lxOCRNL  = 0x8
	lxONOCR  = 0x10
	lxONLRET = 0x20
	lxTABDLY = 0x1800
	lxXTABS  = 0x1800

	lxCS8    = 0x30
	lxCREAD  = 0x80
	lxPARENB = 0x100
	lxHUPCL  = 0x400

	lxISIG    = 0x1
	lxICANON  = 0x2
	lxECHO    = 0x8
	lxECHOE   = 0x10
	lxECHOK   = 0x20
	lxECHONL  = 0x40
	lxNOFLSH  = 0x80
	lxTOSTOP  = 0x100
	lxECHOCTL = 0x200
	lxECHOPRT = 0x400
	lxECHOKE  = 0x800
	lxFLUSHO  = 0x1000
	lxIEXTEN  = 0x8000
)

// c_cc indices.
const (
	lxVINTR    = 0
	lxVQUIT    = 1
	lxVERASE   = 2
	lxVKILL    = 3
	lxVEOF     = 4
	lxVTIME    = 5
	lxVMIN     = 6
	lxVSTART   = 8
	lxVSTOP    = 9
	lxVSUSP    = 10
	lxVEOL     = 11
	lxVREPRINT = 12
	lxVDISCARD = 13
	lxVWERASE  = 14
	lxVLNEXT   = 15
	lxVEOL2    = 16
)

const (
	lxCSIZE  = 0x30
	lxCBAUD  = 0x100f
	lxBOTHER = 0x1000
	lxCIBAUD = lxCBAUD << 16
	lxNCCS   = 19
	lxB38400 = 0xf
)

// lxBauds lists the speed of each Linux Bxxx code, indexed by code (the
// CBAUDEX codes 0x1001.. follow the 16 classic ones).
var lxBauds = []uint64{
	0, 50, 75, 110, 134, 150, 200, 300, 600, 1200, 1800, 2400, 4800, 9600, 19200, 38400,
	57600, 115200, 230400, 460800, 500000, 576000, 921600, 1000000, 1152000, 1500000,
	2000000, 2500000, 3000000, 3500000, 4000000,
}

func baudCode(speed uint64) (uint64, bool) {
	for i, s := range lxBauds {
		if s == speed {
			if i < 16 {
				return uint64(i), true
			}
			return lxBOTHER | uint64(i-15), true
		}
	}
	return lxBOTHER, false
}

func codeBaud(code uint64) uint64 {
	if code&lxBOTHER == 0 {
		return lxBauds[code&0xf]
	}
	if i := 15 + int(code&0xf); code&0xf != 0 && i < len(lxBauds) {
		return lxBauds[i]
	}
	return 0
}

// lxTermios is Linux's kernel struct termios2; TCGETS uses its first 36 bytes.
type lxTermios struct {
	iflag, oflag, cflag, lflag uint32
	line                       byte
	cc                         [lxNCCS]byte
	ispeed, ospeed             uint32
}

func (t *lxTermios) encode(two bool) []byte {
	n := 36
	if two {
		n = 44
	}
	le := binary.LittleEndian
	b := make([]byte, n)
	le.PutUint32(b[0:], t.iflag)
	le.PutUint32(b[4:], t.oflag)
	le.PutUint32(b[8:], t.cflag)
	le.PutUint32(b[12:], t.lflag)
	b[16] = t.line
	copy(b[17:36], t.cc[:])
	if two {
		le.PutUint32(b[36:], t.ispeed)
		le.PutUint32(b[40:], t.ospeed)
	}
	return b
}

func decodeLxTermios(b []byte) *lxTermios {
	le := binary.LittleEndian
	t := &lxTermios{iflag: le.Uint32(b), oflag: le.Uint32(b[4:]), cflag: le.Uint32(b[8:]), lflag: le.Uint32(b[12:]), line: b[16]}
	copy(t.cc[:], b[17:36])
	if len(b) >= 44 {
		t.ispeed, t.ospeed = le.Uint32(b[36:]), le.Uint32(b[40:])
	}
	return t
}

// stdTermios is a new Linux terminal's settings (tty_std_termios): cooked
// input with echo and signals, \n output as \r\n, 38400 baud.
func stdTermios() lxTermios {
	t := lxTermios{
		iflag:  lxICRNL | lxIXON,
		oflag:  lxOPOST | lxONLCR,
		cflag:  lxB38400 | lxCS8 | lxCREAD | lxHUPCL,
		lflag:  lxISIG | lxICANON | lxECHO | lxECHOE | lxECHOK | lxECHOCTL | lxECHOKE | lxIEXTEN,
		ispeed: 38400,
		ospeed: 38400,
	}
	copy(t.cc[:], "\003\034\177\025\004\000\001\000\021\023\032\000\022\017\027\026\000")
	return t
}

// setFrom applies a TCSETS/TCSETS2 argument the way Linux stores it: the
// speeds follow the Bxxx codes in c_cflag unless they are BOTHER (termios2).
func (t *lxTermios) setFrom(n *lxTermios, two bool) {
	*t = *n
	ocode := uint64(n.cflag) & lxCBAUD
	icode := (uint64(n.cflag) & lxCIBAUD) >> 16
	if !two || ocode != lxBOTHER {
		t.ospeed = uint32(codeBaud(ocode))
	}
	switch {
	case icode == 0:
		t.ispeed = t.ospeed
	case !two || icode != lxBOTHER:
		t.ispeed = uint32(codeBaud(icode))
	}
}
