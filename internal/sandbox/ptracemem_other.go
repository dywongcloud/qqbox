//go:build darwin || windows

package sandbox

// ptraceAccess reads or writes the tracee g's memory through its own channel
// (macOS) or process handle (Windows), both of which readMem/writeMem use. On
// macOS a ptrace-stopped guest's stub thread is parked in its reply loop -- it
// posted the stopping syscall and awaits the supervisor -- so it services the
// peek or poke without resuming the guest. Forced writes to read-only text (a
// software breakpoint) are not done here; that is gdb-level, and the Mac is
// arm64, which cannot single-step regardless. The stub-overlap guard is applied
// by the caller.
func ptraceAccess(g *dguest, addr uint64, b []byte, write bool) error {
	if write {
		return g.forceWriteMem(addr, b)
	}
	data, err := g.readMem(addr, len(b))
	if err != nil {
		return err
	}
	copy(b, data)
	return nil
}

// The stub text has no single fixed range here, so canStep uses the per-host
// ptraceInternalPC instead of [ptStubLo, ptStubHi); these stay zero.
// ptraceStepOK and ptraceInternalPC are defined per host (ptracestep_*.go).
var (
	ptStubLo uint64
	ptStubHi uint64
)

// ptraceSP is read from the published register frame instead of a saved slot;
// it is informational (GET_SYSCALL_INFO) for a strace-level tracer.
func ptraceSP(*dguest) uint64 { return 0 }
