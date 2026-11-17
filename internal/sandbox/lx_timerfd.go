//go:build darwin || linux || windows

package sandbox

import (
	"os"
	"sync"
	"time"
)

// timerfd: a counter of expirations driven by a Go timer, whose readiness is
// mirrored onto a host pipe (one byte present exactly while the count is
// non-zero) so epoll's kqueue and poll can watch it, as for eventfd.

const (
	lxTimerfdCreate  = 85
	lxTimerfdSettime = 86
	lxTimerfdGettime = 87

	lxTfdTimerAbstime     = 1
	lxTfdTimerCancelOnSet = 2

	itimerspecSize = 32 // {it_interval, it_value}, each a 16-byte timespec
)

func init() {
	syscalls[lxTimerfdCreate] = func(g *dguest, a [6]uint64) int64 {
		return g.timerfdCreate(int64(int32(a[0])), int64(int32(a[1])))
	}
	syscalls[lxTimerfdSettime] = func(g *dguest, a [6]uint64) int64 {
		return g.timerfdSettime(int64(int32(a[0])), int64(int32(a[1])), a[2], a[3])
	}
	syscalls[lxTimerfdGettime] = func(g *dguest, a [6]uint64) int64 {
		return g.timerfdGettime(int64(int32(a[0])), a[1])
	}
}

type timerfd struct {
	pollNote
	mu       sync.Mutex
	clock    int64
	interval time.Duration
	deadline time.Time // next expiry; zero when disarmed
	count    uint64
	gen      uint64 // bumped on every settime, so a stale timer callback is ignored
	t        *time.Timer
	w        *os.File // readiness pipe write end
	wake     chan struct{}
	closed   bool
}

func (g *dguest) timerfdCreate(clock, flags int64) int64 {
	switch clock {
	case 0, 1, 7, 8, 9: // REALTIME, MONOTONIC, BOOTTIME, REALTIME_ALARM, BOOTTIME_ALARM
	default:
		return sErr(lxEINVAL)
	}
	if flags&^(lxONonblock|lxOCloexec) != 0 {
		return sErr(lxEINVAL)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return sErr(osErrno(err))
	}
	t := &timerfd{pollNote: g.pollNote(), clock: clock, w: w, wake: make(chan struct{})}
	d := &dfile{f: r, gp: "anon_inode:[timerfd]", stub: -1, tfd: t}
	d.nonblock.Store(flags&lxONonblock != 0)
	fd := g.ft.add(d, 0)
	if flags&lxOCloexec != 0 {
		g.ft.setCloexec(fd, true)
	}
	return int64(fd)
}

func (g *dguest) timerfdOf(fd int64) (*dfile, int64) {
	d, ok := g.ft.get(int(fd))
	if !ok {
		return nil, sErr(lxEBADF)
	}
	if d.tfd == nil {
		return nil, sErr(lxEINVAL)
	}
	return d, 0
}

func putTimespec(b []byte, d time.Duration) {
	d = max(d, 0)
	put64(b, uint64(d/time.Second))
	put64(b[8:], uint64(d%time.Second))
}

// current returns the itimerspec gettime reports: the interval and the time
// left until the next expiry. The caller holds mu.
func (t *timerfd) current() []byte {
	b := make([]byte, itimerspecSize)
	putTimespec(b, t.interval)
	if !t.deadline.IsZero() {
		putTimespec(b[16:], max(time.Until(t.deadline), time.Nanosecond))
	}
	return b
}

func (g *dguest) timerfdGettime(fd int64, cur uint64) int64 {
	d, e := g.timerfdOf(fd)
	if d == nil {
		return e
	}
	d.tfd.mu.Lock()
	b := d.tfd.current()
	d.tfd.mu.Unlock()
	if err := g.writeMem(cur, b); err != nil {
		return sErr(lxEFAULT)
	}
	return 0
}

func (g *dguest) timerfdSettime(fd, flags int64, newAddr, oldAddr uint64) int64 {
	d, e := g.timerfdOf(fd)
	if d == nil {
		return e
	}
	if flags&^(lxTfdTimerAbstime|lxTfdTimerCancelOnSet) != 0 {
		return sErr(lxEINVAL)
	}
	b, err := g.argRead(newAddr, itimerspecSize)
	if err != nil {
		return sErr(lxEFAULT)
	}
	interval, ok1 := readTimespec(b)
	value, ok2 := readTimespec(b[16:])
	if !ok1 || !ok2 {
		return sErr(lxEINVAL)
	}
	t := d.tfd
	t.mu.Lock()
	old := t.current()
	t.gen++
	if t.t != nil {
		t.t.Stop()
		t.t = nil
	}
	t.setCount(0, d)
	t.interval, t.deadline = interval, time.Time{}
	if value != 0 {
		delay := value
		if flags&lxTfdTimerAbstime != 0 {
			delay = value - clockNow(t.clock)
		}
		t.deadline = time.Now().Add(delay)
		gen := t.gen
		t.t = time.AfterFunc(max(delay, 0), func() { t.fire(gen, d) })
	}
	t.mu.Unlock()
	if oldAddr != 0 {
		if err := g.writeMem(oldAddr, old); err != nil {
			return sErr(lxEFAULT)
		}
	}
	return 0
}

// setCount updates the expiration count and its readiness byte. The caller
// holds mu.
func (t *timerfd) setCount(n uint64, d *dfile) {
	switch {
	case t.count == 0 && n != 0:
		t.w.Write([]byte{1})
		close(t.wake)
		t.wake = make(chan struct{})
	case t.count != 0 && n == 0:
		var b [1]byte
		d.f.Read(b[:])
	}
	t.count = n
	t.touch()
}

// fire counts the expiries due (more than one if the timer fell behind a
// periodic interval) and schedules the next.
func (t *timerfd) fire(gen uint64, d *dfile) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if gen != t.gen || t.closed || t.deadline.IsZero() {
		return
	}
	now := time.Now()
	n := uint64(1)
	if t.interval > 0 {
		if late := now.Sub(t.deadline); late > 0 {
			n += uint64(late / t.interval)
		}
		t.deadline = t.deadline.Add(time.Duration(n) * t.interval)
		t.t = time.AfterFunc(max(time.Until(t.deadline), 0), func() { t.fire(gen, d) })
	} else {
		t.deadline = time.Time{}
	}
	t.setCount(t.count+n, d)
}

func (t *timerfd) close() {
	t.mu.Lock()
	t.closed = true
	if t.t != nil {
		t.t.Stop()
	}
	t.w.Close()
	t.mu.Unlock()
}

func (g *dguest) timerfdRead(d *dfile, bufAddr uint64, n int64) int64 {
	if n < 8 {
		return sErr(lxEINVAL)
	}
	t := d.tfd
	for {
		t.mu.Lock()
		if v := t.count; v != 0 {
			t.setCount(0, d)
			t.mu.Unlock()
			var b [8]byte
			put64(b[:], v)
			if err := g.writeMem(bufAddr, b[:]); err != nil {
				return sErr(lxEFAULT)
			}
			return 8
		}
		wake := t.wake
		t.mu.Unlock()
		if d.nonblock.Load() {
			return sErr(lxEAGAIN)
		}
		select {
		case <-wake:
		case <-g.ch.done:
			return sErr(lxEINTR)
		case <-g.ch.stop:
			return sErr(lxEINTR)
		}
	}
}
