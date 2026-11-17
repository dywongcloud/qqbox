//go:build darwin || linux || windows

package sandbox

// Each slot's bounce window, past the signal area of the shared region: guest
// memory moves through it, winSize bytes a round trip. The layout matches the
// stubs (WIN_OFF, WIN_SIZE in stub/stub/macos.zig and stub/stub/windows.zig).
const (
	winOff  = slotsLen + sigAreaLen
	winSize = 256 << 10
	chanLen = winOff + maxSlots*winSize

	// ioChunk bounds the bytes one read or write moves: one window.
	ioChunk = winSize
)

func (g *dguest) window() []byte {
	o := winOff + g.slot*winSize
	return g.mem[o : o+winSize : o+winSize]
}
