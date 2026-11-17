package sandbox

import (
	"sync"

	"golang.org/x/sys/unix"

	"github.com/samyfodil/laboon/stub"
)

// The stub lives in a high window ([stubLo, stubHi), see chan.go). The
// top-level laboon's stub is an ET_EXEC at the primary window (stubLo's
// default), placed there by the kernel exec. A laboon that is itself a guest
// cannot use that window -- its outer's stub already holds it in this very
// address space -- so it launches the PIE stub, which its outer's loader
// places at a free high window (honoring the stub's window note); the actual
// base is learned from the ready handshake (adoptWindow), to any nesting depth.

var (
	stubOnce   sync.Once
	stubBinary []byte
	// outermost is set once chooseStub runs: true when this process launches
	// the primary ET_EXEC stub, which owns the real %gs (so its trampolines can
	// use the %gs anchor). A nested laboon launches a PIE stub whose code the
	// outer rewrites, and whose arch_prctl(ARCH_SET_GS) the outer virtualizes,
	// so it cannot own %gs: its guests take the trap path instead.
	outermost bool
)

// chooseStub returns the stub binary for this process, once: the ET_EXEC at the
// primary window when that window is free (top level), else the PIE for the
// outer to place. The window is set from the stub's reported base, not here.
func chooseStub() []byte {
	stubOnce.Do(func() {
		if windowFree(stubLo) {
			stubBinary = stub.Binary
			outermost = true
		} else {
			stubBinary = stub.BinaryPIE
		}
	})
	return stubBinary
}

// adoptWindow sets the Go-side window to the base the stub reported at ready
// (its own load base): the primary for the top-level ET_EXEC, or the free high
// window the outer placed the PIE at. Called once, before any guest loads, so
// slot addressing and the [stubLo,stubHi) exclusion use the live base.
func adoptWindow(base uint64) {
	slotsAddr = base + 0x1000000000
	stubLo = base
	stubHi = base + stubWinSize
}

// windowFree reports whether the stub window at lo is unused in this process.
// It maps one page there with MAP_FIXED_NOREPLACE -- which never replaces an
// existing mapping, and (MAP_FIXED being unset) never clobbers on a kernel too
// old to know the flag, where it degrades to an ordinary hinted mmap that
// simply lands elsewhere -- and unmaps it. A nested laboon finds the window
// held by its outer's stub.
func windowFree(lo uint64) bool {
	r, _, errno := unix.Syscall6(unix.SYS_MMAP, uintptr(lo), uintptr(pageSize),
		unix.PROT_NONE, unix.MAP_PRIVATE|unix.MAP_ANONYMOUS|unix.MAP_FIXED_NOREPLACE, ^uintptr(0), 0)
	if errno != 0 {
		return false
	}
	unix.Syscall(unix.SYS_MUNMAP, r, uintptr(pageSize), 0)
	return uint64(r) == lo
}
