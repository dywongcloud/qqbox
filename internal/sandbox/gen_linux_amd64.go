package sandbox

import (
	"time"

	"golang.org/x/sys/unix"
)

// Keys for x86-64 calls with no generic form. arch_prctl sets or reads the
// guest's thread pointer, the stub thread's own.
const (
	lxPoll      = 0x4c431000 + 7
	lxSelect    = 0x4c431000 + 23
	lxArchPrctl = 0x4c431000 + 158
	lxTime      = 0x4c431000 + 201
)

func init() {
	syscalls[lxArchPrctl] = func(g *dguest, a [6]uint64) int64 {
		switch a[0] {
		// FS is the guest's thread pointer; it owns %fs.
		case 0x1002, 0x1003: // ARCH_SET_FS, ARCH_GET_FS
			return g.host(unix.SYS_ARCH_PRCTL, a[0], a[1])
		// The stub owns the real %gs as its syscall-trampoline anchor, so the
		// guest's %gs is virtual: kept in its slot's Thread.guest_gs, never set
		// on the CPU. (A guest's own %gs: dereferences would need rewriting;
		// the runtimes we run use %fs and never touch %gs.)
		case 0x1001: // ARCH_SET_GS
			put64(g.mem[g.slot*slotSize+stubGuestGs:], a[1])
			return 0
		case 0x1004: // ARCH_GET_GS
			var b [8]byte
			put64(b[:], le64(g.mem[g.slot*slotSize+stubGuestGs:]))
			if g.writeMem(a[1], b[:]) != nil {
				return sErr(lxEFAULT)
			}
			return 0
		}
		return sErr(lxEINVAL)
	}
	syscalls[lxPoll] = func(g *dguest, a [6]uint64) int64 {
		timeout := time.Duration(-1)
		if ms := int64(int32(a[2])); ms >= 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
		r := g.pollFor(a[0], a[1], timeout, 0)
		if r == sErr(errRestart) {
			r = sErr(errRestartNoHand)
		}
		return r
	}
	syscalls[lxSelect] = func(g *dguest, a [6]uint64) int64 {
		timeout := time.Duration(-1)
		if a[4] != 0 {
			b, err := g.readMem(a[4], 16)
			if err != nil {
				return sErr(lxEFAULT)
			}
			sec, usec := int64(le64(b)), int64(le64(b[8:]))
			if sec < 0 || usec < 0 {
				return sErr(lxEINVAL)
			}
			timeout = time.Duration(sec)*time.Second + time.Duration(min(usec, 999999))*time.Microsecond
		}
		start := time.Now()
		r := g.selectFor(int64(int32(a[0])), a[1], a[2], a[3], timeout, 0)
		if a[4] != 0 && r >= 0 {
			// Linux's select leaves the time left in the timeval.
			left := max(timeout-time.Since(start), 0)
			var b [16]byte
			put64(b[:], uint64(left/time.Second))
			put64(b[8:], uint64(left%time.Second/time.Microsecond))
			g.writeMem(a[4], b[:])
		}
		return r
	}
	syscalls[lxTime] = func(g *dguest, a [6]uint64) int64 {
		now := time.Now().Unix()
		if a[0] != 0 {
			var b [8]byte
			put64(b[:], uint64(now))
			if g.writeMem(a[0], b[:]) != nil {
				return sErr(lxEFAULT)
			}
		}
		return now
	}
}

// x64Memory are the memory calls, which the shared table serves only for the
// Linux stub (the others serve them themselves), by x86-64 number.
var x64Memory = map[uint64]uint64{9: lxMmap, 10: lxMprotect, 11: lxMunmap, 12: lxBrk, 25: lxMremap, 149: lxMlock, 150: lxMunlock}

// toGeneric maps an x86-64 syscall to the shared table's key; the result
// goes back through fromGeneric. pause is a ppoll on nothing.
func toGeneric(nr uint64, a [6]uint64) (uint64, [6]uint64, bool) {
	if nr == 34 {
		return lxPpoll, [6]uint64{}, true
	}
	// rt_sigreturn (x86 15 -> generic 139) is completed by the stub, but it is
	// dispatched here so the stub learns whether this layer owns and admits it
	// (the stub runs every rt_sigreturn past the supervisor for the guest's own
	// seccomp filter and for nesting ownership); without the mapping it would
	// fall through to ENOSYS. The guest filter is evaluated on the raw x86 number.
	if nr == 15 {
		return lxRtSigreturn, a, true
	}
	if g, ok := x64Memory[nr]; ok {
		return g, a, true
	}
	switch nr {
	case 7:
		return lxPoll, a, true
	case 23:
		return lxSelect, a, true
	case 158:
		return lxArchPrctl, a, true
	case 201:
		return lxTime, a, true
	}
	return x64Generic(nr, a)
}

func fromGeneric(nr uint64, a [6]uint64, r int64) int64 { return x64Result(nr, a, r) }
