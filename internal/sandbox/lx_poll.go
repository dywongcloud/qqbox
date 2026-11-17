//go:build darwin || linux || windows

package sandbox

import "time"

// ppoll and pselect6 as the guest calls them; each host implements the wait
// (ppoll, pselect6 on its own readiness source).

const (
	lxPselect6 = 72
	lxPpoll    = 73

	lxPollIn     = 0x1
	lxPollPri    = 0x2
	lxPollOut    = 0x4
	lxPollErr    = 0x8
	lxPollHup    = 0x10
	lxPollNval   = 0x20
	lxPollRdnorm = 0x40
	lxPollWrnorm = 0x100
	lxPollRdhup  = 0x2000
)

func init() {
	syscalls[lxPpoll] = func(g *dguest, a [6]uint64) int64 {
		if a[3] != 0 {
			if e := g.sigSwapMask(a[3], a[4]); e != 0 {
				return sErr(e)
			}
		}
		return g.ppoll(a[0], a[1], a[2])
	}
	syscalls[lxPselect6] = func(g *dguest, a [6]uint64) int64 {
		if a[5] != 0 { // {const sigset_t *ss; size_t ss_len}
			b, err := g.argRead(a[5], 16)
			if err != nil {
				return sErr(lxEFAULT)
			}
			if ss := le64(b); ss != 0 {
				if e := g.sigSwapMask(ss, le64(b[8:])); e != 0 {
					return sErr(e)
				}
			}
		}
		return g.pselect6(int64(int32(a[0])), a[1], a[2], a[3], a[4])
	}
}

// timeoutAt reads a guest timespec (NULL: no limit).
func (g *dguest) timeoutAt(addr uint64) (time.Duration, lxErrno) {
	if addr == 0 {
		return -1, 0
	}
	b, err := g.argRead(addr, 16)
	if err != nil {
		return 0, lxEFAULT
	}
	d, ok := readTimespec(b)
	if !ok {
		return 0, lxEINVAL
	}
	return d, 0
}

// writeRemaining stores the time left back in the guest's timespec, as the
// raw Linux syscalls do.
func (g *dguest) writeRemaining(tsp uint64, timeout time.Duration, start time.Time) {
	if tsp == 0 || timeout < 0 {
		return
	}
	left := max(timeout-time.Since(start), 0)
	var b [16]byte
	put64(b[:], uint64(left/time.Second))
	put64(b[8:], uint64(left%time.Second))
	g.putOut(tsp, b[:])
}
