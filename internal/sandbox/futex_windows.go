//go:build windows

package sandbox

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Futexes on shared memory (MAP_SHARED views, which a fork child shares)
// are served here, keyed by the view's section and the word's offset in it,
// so waiters in different stubs meet. Private futexes, and futexes on
// private memory, stay in the stub (stub/windows/futex.zig).

const lcFutex = 0x4c430026 // (uaddr, op, val, timeout, key, offset)

type futexKey struct{ key, off uint64 }

type futexTable struct {
	mu      sync.Mutex
	waiters map[futexKey][]chan struct{}
}

// sectionKeys numbers the sections made for shared anonymous memory; a file
// section is named by its file instead.
var sectionKeys atomic.Uint64

func init() { sectionKeys.Store(1 << 63) }

// futexFileKey names the file behind h for shared futexes: its volume and file
// index, as Linux keys a shared futex by inode.
func futexFileKey(h windows.Handle) uint64 {
	var fi windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &fi) != nil {
		return sectionKeys.Add(1)
	}
	k := uint64(fi.VolumeSerialNumber)<<32 ^ uint64(fi.FileIndexHigh)<<16 ^ uint64(fi.FileIndexLow)
	return k&^(1<<63) | 1
}

const (
	futexWait       = 0
	futexWake       = 1
	futexWaitBitset = 9
	futexClockRT    = 256
)

func (ft *futexTable) serve(dg *dguest, uaddr, op, val, timeout, key, off uint64) int64 {
	k := futexKey{key, off}
	switch op &^ (128 | futexClockRT) {
	case futexWait, futexWaitBitset:
		return ft.wait(dg, k, uaddr, uint32(val), timeout, op&^(128|futexClockRT) == futexWaitBitset, op&futexClockRT != 0)
	}
	// A wake; requeue and wake-op wake on uaddr, and the waiters recheck.
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ws := ft.waiters[k]
	n := min(int(uint32(val)), len(ws))
	if op&^(128|futexClockRT) != futexWake && op&^(128|futexClockRT) != 10 {
		n = len(ws)
	}
	for _, c := range ws[:n] {
		close(c)
	}
	if n == len(ws) {
		delete(ft.waiters, k)
	} else {
		ft.waiters[k] = ws[n:]
	}
	return int64(n)
}

func (ft *futexTable) wait(dg *dguest, k futexKey, uaddr uint64, val uint32, timeout uint64, absolute, realtime bool) int64 {
	var deadline time.Time
	if timeout != 0 {
		b, err := dg.readMem(timeout, 16)
		if err != nil {
			return sErr(lxEFAULT)
		}
		sec, nsec := int64(le64(b)), int64(le64(b[8:]))
		if sec < 0 || nsec < 0 || nsec >= 1e9 {
			return sErr(lxEINVAL)
		}
		d := time.Duration(sec)*time.Second + time.Duration(nsec)
		if absolute {
			clock := int64(1) // CLOCK_MONOTONIC
			if realtime {
				clock = 0
			}
			d -= clockNow(clock)
		}
		deadline = time.Now().Add(d)
	}
	ft.mu.Lock()
	b, err := dg.readMem(uaddr, 4)
	if err != nil {
		ft.mu.Unlock()
		return sErr(lxEFAULT)
	}
	if *(*uint32)(unsafe.Pointer(&b[0])) != val {
		ft.mu.Unlock()
		return sErr(lxEAGAIN)
	}
	me := make(chan struct{})
	if ft.waiters == nil {
		ft.waiters = map[futexKey][]chan struct{}{}
	}
	ft.waiters[k] = append(ft.waiters[k], me)
	ft.mu.Unlock()
	leave := func(r int64) int64 {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		select {
		case <-me:
			return 0 // woken as we gave up: take the wake
		default:
		}
		ws := ft.waiters[k]
		for i, c := range ws {
			if c == me {
				ft.waiters[k] = append(ws[:i:i], ws[i+1:]...)
				break
			}
		}
		if len(ft.waiters[k]) == 0 {
			delete(ft.waiters, k)
		}
		return r
	}
	var kick chan struct{}
	if t := dg.sigs().thread(dg.pid); t != nil {
		kick = t.kick()
	}
	var timer <-chan time.Time
	if !deadline.IsZero() {
		tm := time.NewTimer(time.Until(deadline))
		defer tm.Stop()
		timer = tm.C
	}
	for {
		// A signal or the stub's end after these checks kicks the thread,
		// and the kick channel holds it.
		if dg.sigIntr() {
			return leave(restartSys)
		}
		if dg.ch.dead() {
			return leave(sErr(lxEINTR))
		}
		select {
		case <-me:
			return 0
		case <-timer:
			return leave(sErr(lxETIMEDOUT))
		case <-kick:
		case <-dg.ch.done:
			return leave(sErr(lxEINTR))
		case <-dg.ch.stop:
			return leave(sErr(lxEINTR))
		}
	}
}
