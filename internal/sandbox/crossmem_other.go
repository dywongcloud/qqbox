//go:build linux || windows

package sandbox

// On Linux and Windows the supervisor reaches any guest process's memory
// directly -- Linux through process_vm on its host pid, Windows through its
// process handle -- so a cross-process copy for process_vm is just the target's
// own readMem/writeMem. caller is unused here; macOS needs it.

func procVmRead(caller, target *dguest, addr uint64, n int) ([]byte, lxErrno) {
	b, err := target.readMem(addr, n)
	if err != nil {
		return nil, lxEFAULT
	}
	return b, 0
}

func procVmWrite(caller, target *dguest, addr uint64, data []byte) lxErrno {
	if target.writeMemCross(addr, data) != nil {
		return lxEFAULT
	}
	return 0
}
