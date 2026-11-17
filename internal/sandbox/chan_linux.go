package sandbox

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// The shared layer's channel and guest-memory hooks on Linux. The handshake
// itself (a shared futex on page.state) is chan.go's; guest memory is the
// stub process's own, read and written with process_vm_readv/writev.

// hostChan is the channel as the shared code holds it.
type hostChan = channel

// wake unblocks the supervisor's own wait on this slot.
func (c *channel) wake() {
	unix.Syscall6(unix.SYS_FUTEX, uintptr(unsafe.Pointer(&c.page.state)), futexWake, 1, 0, 0, 0)
}

// The slot's scratch space for the stub's replies, past the stub's own
// thread state.
const lxScratchOff = stubRecOff + stubLocalLen

func (g *dguest) scratch() []byte {
	o := g.slot*slotSize + lxScratchOff
	return g.mem[o : (g.slot+1)*slotSize]
}

// scratchAddr is where the stub sees scratch().
func (g *dguest) scratchAddr() uint64 { return slotsAddr + uint64(g.slot*slotSize+lxScratchOff) }

// strPage bounds each read of a guest C string: a peek past it could reach
// an unmapped page and fail where the string itself is readable.
const strPage = 4096

func (g *dguest) readMem(addr uint64, n int) ([]byte, error) {
	b := make([]byte, n)
	return b, g.mp.read(addr, b)
}

func (g *dguest) writeMem(addr uint64, data []byte) error { return g.mp.write(addr, data) }

// writeMemCross is writeMem into another process's memory.
func (g *dguest) writeMemCross(addr uint64, data []byte) error { return g.writeMem(addr, data) }

// ioBuf returns a buffer for up to n bytes on their way to or from guest
// memory: the slot's window, which only this thread uses.
func (g *dguest) ioBuf(n int) []byte {
	g.flushOuts() // the window may hold results not yet copied out
	return g.window()[:min(n, winSize)]
}

func (g *dguest) putBuf(addr uint64, b []byte) error { return g.writeMem(addr, b) }

func (g *dguest) getBuf(addr uint64, b []byte) error { return g.mp.read(addr, b) }

// ended is false: a Linux stub's end closes the channel's stop (dead).
func (c *channel) ended() bool { return false }
