//go:build darwin

package sandbox

// On macOS a guest process is a separate task whose memory the supervisor
// cannot read directly; it reads through that process's channel, which only
// works while the process is servicing one. When the target shares the caller's
// address space -- the same task: the caller itself or a CLONE_VM thread
// sibling, which share one memProc -- the caller's own channel reaches it, and
// the caller is already servicing this call. A target in a different task needs
// its Mach task port, which the supervisor does not yet hold, so the copy is
// refused with EPERM rather than hanging on a channel no one services.
// Acquiring that task port -- the substrate for ptrace too -- is the next step.

func procVmRead(caller, target *dguest, addr uint64, n int) ([]byte, lxErrno) {
	if target.mp != caller.mp {
		return nil, lxEPERM
	}
	b, err := caller.readMem(addr, n)
	if err != nil {
		return nil, lxEFAULT
	}
	return b, 0
}

func procVmWrite(caller, target *dguest, addr uint64, data []byte) lxErrno {
	if target.mp != caller.mp {
		return lxEPERM
	}
	if caller.writeMem(addr, data) != nil {
		return lxEFAULT
	}
	return 0
}

// ptrace is wired and verified on macOS (strace-level; arm64 cannot single-step).
const ptraceEnabled = true
