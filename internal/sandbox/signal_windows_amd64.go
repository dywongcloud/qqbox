//go:build windows && amd64

package sandbox

// contextFull = CONTEXT_AMD64 | CONTROL | INTEGER | FLOATING_POINT: the GPRs,
// rip/rsp/eflags, and the FP/SIMD state the sigframe carries.
const contextFull = 0x10000B

// winContext is the x86-64 CONTEXT (winnt.h); x/sys/windows does not define it.
// Its size and field offsets are load-bearing: the stub reads the stolen copy
// at the same layout (stub/windows/sig.zig, ctxFromWin).
type winContext struct {
	P1Home, P2Home, P3Home, P4Home, P5Home, P6Home uint64
	ContextFlags, MxCsr                            uint32
	SegCs, SegDs, SegEs, SegFs, SegGs, SegSs       uint16
	EFlags                                         uint32
	Dr0, Dr1, Dr2, Dr3, Dr6, Dr7                   uint64
	Rax, Rcx, Rdx, Rbx, Rsp, Rbp, Rsi, Rdi         uint64
	R8, R9, R10, R11, R12, R13, R14, R15           uint64
	Rip                                            uint64
	FltSave                                        [512]byte
	VectorRegister                                 [26][16]byte
	VectorControl                                  uint64
	DebugControl                                   uint64
	LastBranchToRip, LastBranchFromRip             uint64
	LastExceptionToRip, LastExceptionFromRip       uint64
}

func (c *winContext) pc() uint64      { return c.Rip }
func (c *winContext) setPC(pc uint64) { c.Rip = pc }

// redirect points c at entry with arg as its first argument, on the stack
// whose top is top: a call's frame, the return address slot left below.
func (c *winContext) redirect(entry, arg, top uint64) {
	c.Rip = entry
	c.Rcx = arg
	c.Rsp = top - 8
}

// afterSyscall reports whether insn, the four bytes before a pc, end with a
// syscall instruction (0f 05): the thread is parked in the kernel.
func afterSyscall(insn [4]byte) bool { return insn[2] == 0x0f && insn[3] == 0x05 }
