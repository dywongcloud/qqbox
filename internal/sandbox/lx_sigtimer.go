//go:build darwin || linux || windows

package sandbox

import (
	"math"
	"time"
)

// Interval timers (setitimer/getitimer, which alarm uses) and POSIX timers
// (timer_create & co), per process, on Go timers. On expiry they post their
// signal through the same routing as kill. ITIMER_VIRTUAL and ITIMER_PROF run
// on wall-clock time: the supervisor cannot see a guest's CPU time.

const (
	sigevSignal   = 0
	sigevNone     = 1
	sigevThreadID = 4
	timerAbstime  = 1
)

type ptimer struct {
	sr       *sigRun
	p        *sigProc
	id       int
	sig      int // 0: no signal (SIGEV_NONE)
	tid      int // SIGEV_THREAD_ID target
	value    uint64
	code     int32
	clock    int32
	t        *time.Timer
	gen      int
	next     time.Time // zero: disarmed
	interval time.Duration
	overrun  int // expiries missed by the signal last sent
	consumed int // the overrun of the signal before it, already taken
}

func init() {
	syscalls[lxSetitimer] = sysSetitimer
	syscalls[lxGetitimer] = sysGetitimer
	syscalls[lxTimerCreate] = sysTimerCreate
	syscalls[lxTimerSettime] = sysTimerSettime
	syscalls[lxTimerGettime] = sysTimerGettime
	syscalls[lxTimerGetoverrun] = sysTimerGetoverrun
	syscalls[lxTimerDelete] = sysTimerDelete
}

// disarm stops pt; the caller holds sr.mu.
func (pt *ptimer) disarm() {
	pt.gen++
	if pt.t != nil {
		pt.t.Stop()
		pt.t = nil
	}
	pt.next = time.Time{}
}

// arm starts pt to fire after d and then every interval (0: once); d <= 0
// with no interval disarms it. The caller holds sr.mu.
func (pt *ptimer) arm(d, interval time.Duration, disarm bool) {
	pt.disarm()
	pt.interval = interval
	pt.overrun, pt.consumed = 0, 0
	if disarm {
		return
	}
	pt.next = time.Now().Add(d)
	gen := pt.gen
	pt.t = time.AfterFunc(max(d, 0), func() { pt.fire(gen) })
}

func (pt *ptimer) remaining() time.Duration {
	if pt.next.IsZero() {
		return 0
	}
	return max(time.Until(pt.next), time.Nanosecond)
}

func (pt *ptimer) fire(gen int) {
	sr := pt.sr
	sr.mu.Lock()
	if pt.gen != gen || pt.p.dead || sr.closed {
		sr.mu.Unlock()
		return
	}
	sr.bg.Add(1)
	defer sr.bg.Done()
	missed := 0
	if pt.interval > 0 {
		pt.next = pt.next.Add(pt.interval)
		if late := time.Since(pt.next); late >= 0 {
			n := int(late/pt.interval) + 1
			missed = n
			pt.next = pt.next.Add(time.Duration(n) * pt.interval)
		}
		pt.t = time.AfterFunc(time.Until(pt.next), func() { pt.fire(gen) })
	} else {
		pt.next = time.Time{}
		pt.t = nil
	}
	var th *sigThread
	if pt.tid != 0 {
		th = sr.threads[pt.tid]
	}
	sig, p := pt.sig, pt.p
	si := sigInfo{code: pt.code, b: pt.value}
	if pt.code == siTimer {
		si.a = uint64(uint32(pt.id)) | uint64(uint32(missed))<<32
	}
	sr.mu.Unlock()
	if sig == 0 || (pt.tid != 0 && th == nil) {
		return
	}
	// A timer has one queued signal: expiries while it is still pending
	// only count as overruns (even for a real-time signal), which the
	// queued siginfo's si_overrun follows.
	if sr.isPending(p, th, sig) || sr.send(p, th, sig, si, nil) != sigQueued {
		sr.mu.Lock()
		pt.overrun += missed + 1
		n := pt.overrun
		sr.mu.Unlock()
		if pt.code == siTimer {
			sr.setOverrun(p, th, sig, pt.id, n)
		}
	} else {
		sr.mu.Lock()
		pt.consumed, pt.overrun = pt.overrun, missed
		sr.mu.Unlock()
	}
}

// setOverrun updates the overrun count in the pending siginfo of timer id.
func (sr *sigRun) setOverrun(p *sigProc, th *sigThread, sig, id, n int) {
	q := &p.q
	if th != nil {
		q = &th.q
	}
	sr.sq.Lock()
	defer sr.sq.Unlock()
	if si := &q.info[sig-1]; q.bits&sigBit(sig) != 0 && si.code == siTimer && uint32(si.a) == uint32(id) {
		si.a = uint64(uint32(id)) | uint64(uint32(n))<<32
	}
}

// durSat is sec seconds plus sub nanoseconds, saturated at the longest
// Duration (about 292 years), which stands for never.
func durSat(sec, sub int64) time.Duration {
	if sec > (math.MaxInt64-sub)/int64(time.Second) {
		return math.MaxInt64
	}
	return time.Duration(sec)*time.Second + time.Duration(sub)
}

func readTimeval(b []byte) (time.Duration, bool) {
	sec, usec := int64(le64(b)), int64(le64(b[8:]))
	if sec < 0 || usec < 0 || usec >= 1e6 {
		return 0, false
	}
	return durSat(sec, usec*int64(time.Microsecond)), true
}

func readTimespec(b []byte) (time.Duration, bool) {
	sec, nsec := int64(le64(b)), int64(le64(b[8:]))
	if sec < 0 || nsec < 0 || nsec >= 1e9 {
		return 0, false
	}
	return durSat(sec, nsec), true
}

// putPair writes {interval, value} as two timevals (us = true) or timespecs.
func putPair(interval, value time.Duration, us bool) []byte {
	b := make([]byte, 32)
	for i, d := range []time.Duration{interval, value} {
		sub := int64(d % time.Second)
		if us {
			sub = (sub + 999) / 1000
		}
		put64(b[i*16:], uint64(d/time.Second))
		put64(b[i*16+8:], uint64(sub))
	}
	return b
}

func (g *dguest) itimer(which uint64) *ptimer {
	sr := g.sigs()
	p := sr.procs[g.tgid]
	if p == nil {
		return nil
	}
	if p.itimers[which] == nil {
		sig := [3]int{sigALRM, sigVTALRM, sigPROF}[which]
		p.itimers[which] = &ptimer{sr: sr, p: p, sig: sig, code: siKernel}
	}
	return p.itimers[which]
}

func sysSetitimer(g *dguest, a [6]uint64) int64 {
	if a[0] > 2 {
		return sErr(lxEINVAL)
	}
	var interval, value time.Duration
	if a[1] != 0 {
		b, err := g.readMem(a[1], 32)
		if err != nil {
			return sErr(lxEFAULT)
		}
		var ok1, ok2 bool
		interval, ok1 = readTimeval(b)
		value, ok2 = readTimeval(b[16:])
		if !ok1 || !ok2 {
			return sErr(lxEINVAL)
		}
	}
	sr := g.sigs()
	sr.mu.Lock()
	pt := g.itimer(a[0])
	if pt == nil {
		sr.mu.Unlock()
		return sErr(lxESRCH)
	}
	old := putPair(pt.interval, pt.remaining(), true)
	pt.arm(value, interval, value == 0)
	sr.mu.Unlock()
	if a[2] != 0 {
		if err := g.writeMem(a[2], old); err != nil {
			return sErr(lxEFAULT)
		}
	}
	return 0
}

func sysGetitimer(g *dguest, a [6]uint64) int64 {
	if a[0] > 2 {
		return sErr(lxEINVAL)
	}
	sr := g.sigs()
	sr.mu.Lock()
	pt := g.itimer(a[0])
	if pt == nil {
		sr.mu.Unlock()
		return sErr(lxESRCH)
	}
	cur := putPair(pt.interval, pt.remaining(), true)
	sr.mu.Unlock()
	if err := g.writeMem(a[1], cur); err != nil {
		return sErr(lxEFAULT)
	}
	return 0
}

func timerClockOK(clk int32) bool {
	switch clk {
	case 0, 1, 2, 3, 7, 8, 9, 11: // REALTIME, MONOTONIC, CPU clocks, BOOTTIME, *_ALARM, TAI
		return true
	}
	return false
}

func sysTimerCreate(g *dguest, a [6]uint64) int64 {
	clk := int32(a[0])
	if !timerClockOK(clk) {
		return sErr(lxEINVAL)
	}
	sr := g.sigs()
	pt := &ptimer{sr: sr, sig: sigALRM, code: siTimer, clock: clk}
	notify := int32(sigevSignal)
	valueSet := false
	if a[1] != 0 {
		b, err := g.readMem(a[1], 64)
		if err != nil {
			return sErr(lxEFAULT)
		}
		pt.value, valueSet = le64(b), true
		pt.sig = int(int32(le32(b[8:])))
		notify = int32(le32(b[12:]))
		switch notify {
		case sigevNone:
			pt.sig = 0
		case sigevSignal, sigevThreadID:
			if pt.sig < 1 || pt.sig > 64 {
				return sErr(lxEINVAL)
			}
		default:
			return sErr(lxEINVAL)
		}
		if notify == sigevThreadID {
			pt.tid = int(int32(le32(b[16:])))
			if t := sr.thread(pt.tid); t == nil || t.tgid != g.tgid {
				return sErr(lxEINVAL)
			}
		}
	}
	sr.mu.Lock()
	p := sr.procs[g.tgid]
	if p == nil {
		sr.mu.Unlock()
		return sErr(lxESRCH)
	}
	pt.p, pt.id = p, p.nextTimer
	p.nextTimer++
	if !valueSet {
		pt.value = uint64(pt.id)
	}
	p.timers[pt.id] = pt
	sr.mu.Unlock()
	var b [4]byte
	put32(b[:], uint32(pt.id))
	if err := g.writeMem(a[2], b[:]); err != nil {
		g.timerDelete(pt.id)
		return sErr(lxEFAULT)
	}
	return 0
}

// timer returns the calling process's POSIX timer id; the caller holds sr.mu.
func (g *dguest) timer(id uint64) *ptimer {
	if p := g.sigs().procs[g.tgid]; p != nil {
		return p.timers[int(int32(id))]
	}
	return nil
}

func (g *dguest) timerDelete(id int) bool {
	sr := g.sigs()
	sr.mu.Lock()
	defer sr.mu.Unlock()
	p := sr.procs[g.tgid]
	if p == nil || p.timers[id] == nil {
		return false
	}
	p.timers[id].disarm()
	delete(p.timers, id)
	return true
}

func sysTimerSettime(g *dguest, a [6]uint64) int64 {
	if a[2] == 0 {
		return sErr(lxEINVAL)
	}
	b, err := g.readMem(a[2], 32)
	if err != nil {
		return sErr(lxEFAULT)
	}
	interval, ok1 := readTimespec(b)
	value, ok2 := readTimespec(b[16:])
	if !ok1 || !ok2 {
		return sErr(lxEINVAL)
	}
	disarm := value == 0
	sr := g.sigs()
	sr.mu.Lock()
	pt := g.timer(a[0])
	if pt == nil {
		sr.mu.Unlock()
		return sErr(lxEINVAL)
	}
	if a[1]&timerAbstime != 0 && !disarm {
		switch pt.clock {
		case 0, 8, 11: // REALTIME, REALTIME_ALARM, TAI: the wall clock
			value = time.Until(time.Unix(0, int64(value)))
		default:
			value -= monoNow()
		}
	}
	old := putPair(pt.interval, pt.remaining(), false)
	pt.arm(value, interval, disarm)
	sr.mu.Unlock()
	if a[3] != 0 {
		if err := g.writeMem(a[3], old); err != nil {
			return sErr(lxEFAULT)
		}
	}
	return 0
}

func sysTimerGettime(g *dguest, a [6]uint64) int64 {
	sr := g.sigs()
	sr.mu.Lock()
	pt := g.timer(a[0])
	if pt == nil {
		sr.mu.Unlock()
		return sErr(lxEINVAL)
	}
	cur := putPair(pt.interval, pt.remaining(), false)
	sr.mu.Unlock()
	if err := g.writeMem(a[1], cur); err != nil {
		return sErr(lxEFAULT)
	}
	return 0
}

func sysTimerGetoverrun(g *dguest, a [6]uint64) int64 {
	sr := g.sigs()
	sr.mu.Lock()
	defer sr.mu.Unlock()
	pt := g.timer(a[0])
	if pt == nil {
		return sErr(lxEINVAL)
	}
	// It reports the signal last delivered: while a newer one waits, that is
	// the one before it.
	var th *sigThread
	if pt.tid != 0 {
		th = sr.threads[pt.tid]
	}
	if pt.sig != 0 && sr.isPending(pt.p, th, pt.sig) {
		return int64(pt.consumed)
	}
	return int64(pt.overrun)
}

func sysTimerDelete(g *dguest, a [6]uint64) int64 {
	if !g.timerDelete(int(int32(a[0]))) {
		return sErr(lxEINVAL)
	}
	return 0
}
