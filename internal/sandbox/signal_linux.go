package sandbox

import (
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The signal calls the macOS and Windows stubs serve themselves, served here
// for the Linux stub on the same records (lx_signal.go). The stub delivers
// what became deliverable on the way out of each call.

const (
	lxRtSigsuspend   = 133
	lxRtSigpending   = 136
	lxRtSigreturn    = 139
	lxRtSigtimedwait = 137

	ssOnstack    = 1
	ssDisable    = 2
	ssAutodisarm = 1 << 31

	minSigstksz = 2048
)

// The stub's own per-thread state in its slot page (struct Thread in
// stub/zig/sig.zig): the host tid it runs on, and the guest's stack
// pointer at the current syscall.
const (
	stubRecOff     = 128
	stubBusy       = stubRecOff + 0  // Thread.busy
	stubHostTid    = stubRecOff + 4
	stubSp         = stubRecOff + 8
	stubKicked     = stubRecOff + 52 // Thread.kicked
	stubWakeAt     = stubRecOff + 56 // Thread.wake_at
	stubScratchTop = stubRecOff + 64 // Thread.scratch_top
	stubGuestGs    = stubRecOff + 72 // Thread.guest_gs
)

func init() {
	syscalls[lxRtSigprocmask] = func(g *dguest, a [6]uint64) int64 { return g.rtSigprocmask(int(a[0]), a[1], a[2], a[3]) }
	syscalls[lxSigaltstack] = func(g *dguest, a [6]uint64) int64 { return g.sigaltstack(a[0], a[1]) }
	syscalls[lxRtSigpending] = func(g *dguest, a [6]uint64) int64 { return g.rtSigpending(a[0], a[1]) }
	syscalls[lxRtSigsuspend] = func(g *dguest, a [6]uint64) int64 { return g.rtSigsuspend(a[0], a[1]) }
	syscalls[lxRtSigtimedwait] = func(g *dguest, a [6]uint64) int64 { return g.rtSigtimedwait(a[0], a[1], a[2], a[3]) }
}

// threadKicks is true: a kick goes to one host thread (tgkill).
const threadKicks = true

// wakeThread reaches a thread running guest code: a host kick to its stub
// thread, which then takes its signals; and its wake pipe, for a wait in the
// supervisor.
func (sr *sigRun) wakeThread(t *sigThread) {
	t.wakeWrite()
	if tid := atomic.LoadUint32((*uint32)(unsafe.Pointer(&sr.fs.mem[t.slot*slotSize+stubHostTid]))); tid != 0 {
		unix.Tgkill(sr.mainOspidOf(t), int(tid), kickSignal)
	}
}

// mainOspidOf is the host pid of t's stub process.
func (sr *sigRun) mainOspidOf(t *sigThread) int {
	if p := sr.proc(t.tgid); p != nil {
		if pid := sr.ospid(p); pid > 0 {
			return pid
		}
	}
	return sr.mainOspid
}

func (g *dguest) rtSigprocmask(how int, setAddr, oldAddr, size uint64) int64 {
	if size != 8 {
		return sErr(lxEINVAL)
	}
	sr := g.sigs()
	o := tOff(g.slot)
	cur := sr.ld(o + stMask)
	if setAddr != 0 {
		b, err := g.argRead(setAddr, 8)
		if err != nil {
			return sErr(lxEFAULT)
		}
		set := le64(b)
		nm := cur
		switch how {
		case 0: // SIG_BLOCK
			nm |= set
		case 1: // SIG_UNBLOCK
			nm &^= set
		case 2: // SIG_SETMASK
			nm = set
		default:
			return sErr(lxEINVAL)
		}
		sr.st(o+stMask, nm&^killStopMask)
	}
	if oldAddr != 0 {
		var b [8]byte
		put64(b[:], cur)
		if err := g.writeMem(oldAddr, b[:]); err != nil {
			return sErr(lxEFAULT)
		}
	}
	return 0
}

func (g *dguest) sigaltstack(ssAddr, oldAddr uint64) int64 {
	sr := g.sigs()
	o := tOff(g.slot)
	sp := le64(g.mem[g.slot*slotSize+stubSp:])
	ssSp, ssSize := sr.ld(o+stSsSp), sr.ld(o+stSsSize)
	on := ssSize != 0 && sp > ssSp && sp <= ssSp+ssSize
	if oldAddr != 0 {
		flags := atomic.LoadUint32(sr.p32(o + stSsFlags))
		switch {
		case ssSize == 0:
			flags |= ssDisable
		case on:
			flags |= ssOnstack
		}
		var b [24]byte
		put64(b[:], ssSp)
		put32(b[8:], flags)
		put64(b[16:], ssSize)
		if err := g.writeMem(oldAddr, b[:]); err != nil {
			return sErr(lxEFAULT)
		}
	}
	if ssAddr == 0 {
		return 0
	}
	b, err := g.readMem(ssAddr, 24)
	if err != nil {
		return sErr(lxEFAULT)
	}
	if on {
		return sErr(lxEPERM)
	}
	nsp, nflags, nsize := le64(b), le32(b[8:]), le64(b[16:])
	switch {
	case nflags&ssDisable != 0:
		nsp, nsize = 0, 0
	case nflags&^ssAutodisarm != 0:
		return sErr(lxEINVAL)
	case nsize < minSigstksz:
		return sErr(lxENOMEM)
	}
	sr.st(o+stSsSp, nsp)
	sr.st(o+stSsSize, nsize)
	atomic.StoreUint32(sr.p32(o+stSsFlags), nflags&ssAutodisarm)
	return 0
}

func (g *dguest) rtSigpending(setAddr, size uint64) int64 {
	if size != 8 {
		return sErr(lxEINVAL)
	}
	sr := g.sigs()
	o := tOff(g.slot)
	pend := (sr.ld(o+stPending) | sr.ld(pOff(g.procSlot())+spPending)) & sr.ld(o+stMask)
	var b [8]byte
	put64(b[:], pend)
	if err := g.writeMem(setAddr, b[:]); err != nil {
		return sErr(lxEFAULT)
	}
	return 0
}

// rtSigsuspend waits under the given mask until a signal is to be taken; the
// stub delivers it and restores the real mask on the way out.
func (g *dguest) rtSigsuspend(maskAddr, size uint64) int64 {
	if e := g.sigSwapMask(maskAddr, size); e != 0 {
		return sErr(e)
	}
	g.waitSignal(-1)
	return sErr(errRestartNoHand)
}

// waitSignal blocks until a signal is to be taken or d passes (negative: no
// limit).
func (g *dguest) waitSignal(d time.Duration) {
	var deadline time.Time
	if d >= 0 {
		deadline = time.Now().Add(d)
	}
	t := g.sigs().thread(g.pid)
	for !g.sigIntr() {
		if !g.awaitKick(t, deadline) {
			return
		}
	}
}

// awaitKick blocks until t is kicked (a signal for it was sent; the kick
// channel holds one), the guest is torn down, or deadline passes (zero: none).
// It reports whether to look again: false once the deadline or the guest is
// gone.
func (g *dguest) awaitKick(t *sigThread, deadline time.Time) bool {
	var kick chan struct{}
	if t != nil {
		kick = t.kick()
	}
	var expired <-chan time.Time
	if !deadline.IsZero() {
		left := time.Until(deadline)
		if left <= 0 {
			return false
		}
		tm := time.NewTimer(left)
		defer tm.Stop()
		expired = tm.C
	}
	select {
	case <-kick:
		return true
	case <-g.ch.done:
	case <-g.ch.stop:
	case <-expired:
	}
	return false
}

// rtSigtimedwait takes a pending signal in set, waiting for one up to the
// timeout. The signals of set wake the wait although blocked (the waitset).
func (g *dguest) rtSigtimedwait(setAddr, infoAddr, tsAddr, size uint64) int64 {
	if size != 8 {
		return sErr(lxEINVAL)
	}
	b, err := g.argRead(setAddr, 8)
	if err != nil {
		return sErr(lxEFAULT)
	}
	set := le64(b) &^ killStopMask
	d := time.Duration(-1)
	if tsAddr != 0 {
		ts, err := g.argRead(tsAddr, 16)
		if err != nil {
			return sErr(lxEFAULT)
		}
		d = time.Duration(le64(ts))*time.Second + time.Duration(le64(ts[8:]))
	}
	sr := g.sigs()
	o := tOff(g.slot)
	sr.st(o+stWaitset, set)
	defer sr.st(o+stWaitset, 0)
	var deadline time.Time
	if d >= 0 {
		deadline = time.Now().Add(d)
	}
	t := sr.thread(g.pid)
	for {
		if s, si := g.dequeue(set); s != 0 {
			if infoAddr != 0 {
				var ib [128]byte
				put32(ib[:], uint32(s))
				put32(ib[4:], uint32(si.errno))
				put32(ib[8:], uint32(si.code))
				put64(ib[16:], si.a)
				put64(ib[24:], si.b)
				if err := g.writeMem(infoAddr, ib[:]); err != nil {
					return sErr(lxEFAULT)
				}
			}
			return int64(s)
		}
		if g.sigIntr() || g.ch.dead() {
			return sErr(lxEINTR)
		}
		if !g.awaitKick(t, deadline) {
			if g.ch.dead() {
				return sErr(lxEINTR)
			}
			return sErr(lxEAGAIN)
		}
	}
}
