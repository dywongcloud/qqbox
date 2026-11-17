package sandbox

import "os"

// readStreamFaulting reads what fits from a stream into buf, past which the
// guest's buffer cannot be written. The host does not say what such a file
// holds, so where Linux would take its next chunk whole or fail, this takes
// what fits, losing none of it. A pipe, whose buffer the supervisor keeps,
// is exact (pipeVector).
func (g *dguest) readStreamFaulting(f *os.File, buf []byte, nonblock bool) (int, error) {
	if len(buf) == 0 {
		return 0, errFaultRead
	}
	if nonblock {
		return readNonblock(f, buf)
	}
	return g.readIntr(f, buf, kindUnknown)
}

// guestPageSize is the guest's page size, AT_PAGESZ.
func (g *dguest) guestPageSize() int { return pageSize }
