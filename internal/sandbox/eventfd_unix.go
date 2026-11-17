//go:build darwin || linux

package sandbox

import (
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// eventfd is a guest eventfd: the 64-bit counter, plus a host pipe that holds
// one byte exactly while the counter is non-zero (the dfile's f is its read
// end), so a kqueue sees it readable then.
type eventfd struct {
	mu   sync.Mutex
	cond *sync.Cond
	n    uint64
	sem  bool
	w    *os.File
	full bool // the pipe is filled, so the write end polls unwritable
}

const eventfdMax = ^uint64(0) - 1

// setCount sets the counter and mirrors it on the readiness pipe r/w: one
// byte while it is non-zero (readable), the pipe filled while it is at the
// maximum (not writable). Called with mu held.
func (ev *eventfd) setCount(r *os.File, n uint64) {
	was := ev.n
	ev.n = n
	if ev.full && n != eventfdMax {
		drainPipe(r)
		ev.full = false
		was = 0
	}
	switch {
	case n == eventfdMax && !ev.full:
		if was == 0 {
			ev.w.Write([]byte{1})
		}
		fillPipe(ev.w)
		ev.full = true
	case was == 0 && n != 0:
		ev.w.Write([]byte{1})
	case was != 0 && n == 0:
		var b [1]byte
		r.Read(b[:])
	}
}

func fillPipe(f *os.File) {
	rc, err := f.SyscallConn()
	if err != nil {
		return
	}
	buf := make([]byte, 4096)
	rc.Write(func(fd uintptr) bool {
		for {
			if n, err := unix.Write(int(fd), buf); n <= 0 || err != nil {
				return true
			}
		}
	})
}

func drainPipe(f *os.File) {
	rc, err := f.SyscallConn()
	if err != nil {
		return
	}
	buf := make([]byte, 4096)
	rc.Read(func(fd uintptr) bool {
		for {
			if n, err := unix.Read(int(fd), buf); n <= 0 || err != nil {
				return true
			}
		}
	})
}

func (g *dguest) eventfd2(initval uint64, flags int64) int64 {
	// Reject unknown flags, as the kernel does: Chromium's Linux Mojo channel
	// probes this (eventfd2 with all bits set, expecting EINVAL) to gate its
	// shared-memory fast path, and a permissive answer fails its CHECK.
	if flags&^(lxEfdSemaphore|lxONonblock|lxOCloexec) != 0 {
		return sErr(lxEINVAL)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return sErr(osErrno(err))
	}
	ev := &eventfd{n: initval, sem: flags&lxEfdSemaphore != 0, w: w}
	ev.cond = sync.NewCond(&ev.mu)
	if initval != 0 {
		w.Write([]byte{1})
	}
	d := &dfile{f: r, gp: "anon_inode:[eventfd]", stub: -1, ev: ev}
	d.nonblock.Store(flags&lxONonblock != 0)
	fd := g.ft.add(d, 0)
	if flags&lxOCloexec != 0 {
		g.ft.setCloexec(fd, true)
	}
	return int64(fd)
}

func (g *dguest) eventfdRead(d *dfile, bufAddr uint64, n int64) int64 {
	if n < 8 {
		return sErr(lxEINVAL)
	}
	ev := d.ev
	ev.mu.Lock()
	for ev.n == 0 {
		if d.nonblock.Load() {
			ev.mu.Unlock()
			return sErr(lxEAGAIN)
		}
		if !g.condWaitIntr(ev.cond) {
			ev.mu.Unlock()
			return restartSys
		}
	}
	v := ev.n
	if ev.sem {
		v = 1
	}
	ev.setCount(d.f, ev.n-v)
	ev.cond.Broadcast() // a writer blocked on a full counter
	ev.mu.Unlock()
	var b [8]byte
	put64(b[:], v)
	if err := g.putOut(bufAddr, b[:]); err != nil {
		return sErr(lxEFAULT)
	}
	return 8
}

func (g *dguest) eventfdWrite(d *dfile, bufAddr uint64, n int64) int64 {
	if n < 8 {
		return sErr(lxEINVAL)
	}
	b, err := g.argRead(bufAddr, 8)
	if err != nil {
		return sErr(lxEFAULT)
	}
	v := le64(b)
	if v == ^uint64(0) {
		return sErr(lxEINVAL)
	}
	ev := d.ev
	ev.mu.Lock()
	// The counter holds at most 2^64-2: a write that would pass it blocks
	// until a read makes room.
	for ev.n > eventfdMax-v {
		if d.nonblock.Load() {
			ev.mu.Unlock()
			return sErr(lxEAGAIN)
		}
		if !g.condWaitIntr(ev.cond) {
			ev.mu.Unlock()
			return restartSys
		}
	}
	ev.setCount(d.f, ev.n+v)
	ev.cond.Broadcast()
	ev.mu.Unlock()
	return 8
}
