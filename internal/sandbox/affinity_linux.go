package sandbox

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A guest thread's CPU affinity is its host thread's, which is the guest
// thread: the host kernel keeps the mask, checks it and schedules by it, so
// getcpu and the vDSO name a CPU the mask allows, as on Linux.

// maskBufLen holds any kernel's cpumask: NR_CPUS is at most 8192 bits.
const maskBufLen = 1024

// cpumaskSize is the host kernel's cpumask_size(): what sched_getaffinity
// writes, and the most sched_setaffinity reads.
var cpumaskSize = sync.OnceValue(func() uint64 {
	var b [maskBufLen]byte
	r, _, _ := unix.RawSyscall(unix.SYS_SCHED_GETAFFINITY, 0, maskBufLen, uintptr(unsafe.Pointer(&b[0])))
	return uint64(r)
})

// hostTid is the host thread guest thread pid (0: the caller) runs on.
func (g *dguest) hostTid(pid int) (int, bool) {
	t := g
	if pid != 0 {
		gt, ok := g.nsToGlobal(pid)
		if !ok {
			return 0, false
		}
		fs := g.dfs
		fs.mu.Lock()
		p := fs.procs[gt]
		fs.mu.Unlock()
		if p == nil || p.g == nil {
			return 0, false
		}
		t = p.g
	}
	if tid := int(le32(g.mem[t.slot*slotSize+stubHostTid:])); tid != 0 {
		return tid, true
	}
	return t.forkTid, t.forkTid != 0
}

// hostGetaffinity is sched_getaffinity on pid's host thread, or the last
// mask of a process that has exited and is not reaped, its host thread gone.
// A thread that is not there is ESRCH after the size checks, as the kernel
// orders them.
func (g *dguest) hostGetaffinity(pid int, size, addr uint64) (int64, bool) {
	tid, ok := g.hostTid(pid)
	var b [maskBufLen]byte
	r, _, en := unix.Syscall(unix.SYS_SCHED_GETAFFINITY, uintptr(tid), uintptr(size), uintptr(unsafe.Pointer(&b[0])))
	if en != 0 && en != unix.ESRCH {
		return sErr(lxErrno(en)), true
	}
	if !ok || en != 0 {
		m, gone := g.goneMask(pid)
		if !gone {
			return sErr(lxESRCH), true
		}
		if m == nil {
			m = hostMask(0) // the run's first process: the supervisor's own
		}
		r = uintptr(min(cpumaskSize(), size))
		clear(b[:r])
		copy(b[:r], m)
	}
	if g.writeMem(addr, b[:r]) != nil {
		return sErr(lxEFAULT), true
	}
	return int64(r), true
}

// goneMask is the last mask of process pid, when it has exited and is not
// reaped (a zombie, or on its way to one): nil if it never changed from the
// one the run started with, as only the run's first process has none.
func (g *dguest) goneMask(pid int) ([]byte, bool) {
	gt, ok := g.nsToGlobal(pid)
	if !ok {
		return nil, false
	}
	fs := g.dfs
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ps := fs.pt[gt]
	if ps == nil || !ps.zombie && !ps.exiting {
		return nil, false
	}
	return ps.mask, true
}

// hostSetaffinity is sched_setaffinity on pid's host thread, reading the
// mask as the kernel does: up to its cpumask size, before finding the thread.
func (g *dguest) hostSetaffinity(pid int, size, addr uint64) (int64, bool) {
	b, err := g.readMem(addr, int(min(size, cpumaskSize())))
	if err != nil {
		return sErr(lxEFAULT), true
	}
	tid, ok := g.hostTid(pid)
	if !ok {
		return sErr(lxESRCH), true
	}
	var p unsafe.Pointer
	if len(b) > 0 {
		p = unsafe.Pointer(&b[0])
	}
	if _, _, en := unix.Syscall(unix.SYS_SCHED_SETAFFINITY, uintptr(tid), uintptr(len(b)), uintptr(p)); en != 0 {
		return sErr(lxErrno(en)), true
	}
	g.noteMask(pid, tid)
	return 0, true
}

// noteMask keeps the mask host thread tid now has as its process's, when
// guest thread pid (0: the caller) is the process's leader, for its zombie.
func (g *dguest) noteMask(pid, tid int) {
	t := g
	if pid != 0 {
		gt, _ := g.nsToGlobal(pid)
		fs := g.dfs
		fs.mu.Lock()
		p := fs.procs[gt]
		fs.mu.Unlock()
		if p == nil || p.g == nil {
			return
		}
		t = p.g
	}
	if t.pid != t.tgid {
		return
	}
	m := hostMask(tid)
	fs := g.dfs
	fs.mu.Lock()
	t.ps.mask = m
	fs.mu.Unlock()
}

// affinity is the calling thread's mask, for a fork child to take on.
func (g *dguest) affinity() []byte {
	tid, _ := g.hostTid(0)
	return hostMask(tid)
}

// hostMask is host thread tid's mask, nil if it cannot be read.
func hostMask(tid int) []byte {
	b := make([]byte, maskBufLen)
	r, _, en := unix.Syscall(unix.SYS_SCHED_GETAFFINITY, uintptr(tid), maskBufLen, uintptr(unsafe.Pointer(&b[0])))
	if en != 0 {
		return nil
	}
	return b[:r]
}

// setAffinity gives host thread tid the mask m.
func setAffinity(tid int, m []byte) {
	if len(m) > 0 {
		unix.Syscall(unix.SYS_SCHED_SETAFFINITY, uintptr(tid), uintptr(len(m)), uintptr(unsafe.Pointer(&m[0])))
	}
}
