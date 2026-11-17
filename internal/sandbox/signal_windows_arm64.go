//go:build windows && arm64

package sandbox

import "unsafe"

// contextFull = CONTEXT_ARM64 | CONTROL | INTEGER | FLOATING_POINT: the GPRs,
// sp/pc/cpsr, and the FP/SIMD state the sigframe carries.
const contextFull = 0x400007

// winContext is the arm64 CONTEXT (winnt.h); x/sys/windows does not define it.
// Its size and field offsets are load-bearing: the stub reads the stolen copy
// at the same layout (stub/windows/sig_aarch64.zig, ctxFromWin). X[18] is not
// reported: lc_sig_redirect stores the live x18 there.
type winContext struct {
	ContextFlags uint32
	Cpsr         uint32
	X            [31]uint64 // x0-x28, fp, lr
	Sp           uint64
	Pc           uint64
	V            [32][16]byte
	Fpcr, Fpsr   uint32
	Bcr          [8]uint32
	Bvr          [8]uint64
	Wcr          [2]uint32
	Wvr          [2]uint64
}

func (c *winContext) pc() uint64      { return c.Pc }
func (c *winContext) setPC(pc uint64) { c.Pc = pc }

// redirect points c at entry with arg in x0, on the stack whose top is top.
func (c *winContext) redirect(entry, arg, top uint64) {
	c.Pc = entry
	c.X[0] = arg
	c.Sp = top &^ 15
}

var (
	_ [unsafe.Sizeof(winContext{}) - 0x390]struct{}
	_ [0x390 - unsafe.Sizeof(winContext{})]struct{}
)

// afterSyscall reports whether insn, the instruction before a pc, is an svc
// (whose immediate is the service number on Windows): the thread is parked
// in the kernel.
func afterSyscall(insn [4]byte) bool {
	w := uint32(insn[0]) | uint32(insn[1])<<8 | uint32(insn[2])<<16 | uint32(insn[3])<<24
	return w&0xFFE0001F == 0xD4000001
}
