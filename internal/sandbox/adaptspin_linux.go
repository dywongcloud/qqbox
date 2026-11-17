//go:build linux

package sandbox

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

var calibOnce sync.Once

func ensureCalibrated() { calibOnce.Do(func() { parkCostCy.Store(calibrateParkCost()) }) }

// calibrateParkCost measures, in cputicks, the cost of parking on a futex and
// being woken. Two OS threads bounce a word back and forth; the per-round-trip
// time halved is one wake latency. The minimum over several batches discards
// scheduler outliers, leaving the typical cost. It runs once, lazily, on the
// first adaptive wait.
func calibrateParkCost() uint64 {
	const batches, rounds = 8, 32
	var ping, pong uint32
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		close(ready)
		for n := 0; n < batches*rounds; n++ {
			for atomic.LoadUint32(&ping) == 0 {
				unix.Syscall6(unix.SYS_FUTEX, uintptr(unsafe.Pointer(&ping)), futexWait, 0, 0, 0, 0)
			}
			atomic.StoreUint32(&ping, 0)
			atomic.StoreUint32(&pong, 1)
			unix.Syscall6(unix.SYS_FUTEX, uintptr(unsafe.Pointer(&pong)), futexWake, 1, 0, 0, 0)
		}
	}()
	<-ready
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	best := ^uint64(0)
	for b := 0; b < batches; b++ {
		t0 := cputicks()
		for r := 0; r < rounds; r++ {
			atomic.StoreUint32(&ping, 1)
			unix.Syscall6(unix.SYS_FUTEX, uintptr(unsafe.Pointer(&ping)), futexWake, 1, 0, 0, 0)
			for atomic.LoadUint32(&pong) == 0 {
				unix.Syscall6(unix.SYS_FUTEX, uintptr(unsafe.Pointer(&pong)), futexWait, 0, 0, 0, 0)
			}
			atomic.StoreUint32(&pong, 0)
		}
		if d := (cputicks() - t0) / (2 * rounds); d < best {
			best = d
		}
	}
	if best == 0 {
		best = 1
	}
	return best
}
