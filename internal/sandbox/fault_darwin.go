package sandbox

import (
	"os"

	"golang.org/x/sys/unix"
)

// readStreamFaulting reads from a stream (a pipe, a FIFO, a terminal) into
// buf, past which the guest's buffer cannot be written. Linux's copy takes a
// pipe's next chunk whole or not at all: what the stream holds is taken when
// it fits, else none of it, leaving it there, and the read fails. With
// nothing held it waits, as any read does, and at the stream's end gives 0.
// A stream that does not say what it holds gives what fits.
func (g *dguest) readStreamFaulting(f *os.File, buf []byte, nonblock bool) (int, error) {
	fd := hostFd(f)
	for {
		if !nonblock && g.sigIntr() {
			return 0, errSigIntr
		}
		held, err := unix.IoctlGetInt(fd, hostFIONREAD)
		switch {
		case err != nil && len(buf) == 0, err == nil && held > len(buf):
			return 0, errFaultRead
		case err != nil && nonblock:
			return readNonblock(f, buf)
		case err != nil:
			return g.readIntr(f, buf, kindUnknown)
		case held > 0:
			return readNonblock(f, buf[:held])
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, _ := unix.Poll(fds, 0); n > 0 {
			if held, err := unix.IoctlGetInt(fd, hostFIONREAD); err == nil && held == 0 {
				return 0, nil // readable with nothing held: the end of the stream
			}
			continue
		}
		if nonblock {
			return 0, unix.EAGAIN
		}
		if err := g.sigWaitFd(fd, unix.POLLIN); err != nil {
			return 0, err
		}
	}
}
