//go:build darwin || linux

package sandbox

import (
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

// hostSeqpacket is whether the host has AF_UNIX SOCK_SEQPACKET; Darwin does not.
const hostSeqpacket = runtime.GOOS != "darwin"

// seqPair is a connected pair of AF_UNIX SOCK_SEQPACKET sockets on a host
// without them, made of two datagram sockets: each message is one datagram,
// so it keeps its boundaries, truncation and SCM_RIGHTS as a seqpacket does.
// What datagrams lack the pair supplies: once an end is closed (or shut down
// for writing) its peer reads the end of the stream, 0 from every receive as
// on Linux, and writing to a closed end fails with EPIPE. The end is an empty
// datagram, sent again each time a receive takes it, so the peer stays
// readable for poll; the pair keeps both host sockets open (fd) to send it. A
// sender whose peer has no room left gets ENOBUFS rather than blocking, as
// Darwin datagrams do.
type seqPair struct {
	mu      sync.Mutex
	fd      [2]int  // the pair's own descriptors of the two host sockets
	closed  [2]bool // the guest closed that end
	ended   [2]bool // that end will send no more: closed or shut down for writing
	pending [2]bool // the end of the stream is owed to that end: its buffer was full
}

// newSeqPair makes the host datagram pair and the pair's own descriptors.
func newSeqPair() (*seqPair, [2]int, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		return nil, fds, err
	}
	p := &seqPair{}
	for i, fd := range fds {
		d, err := unix.Dup(fd)
		if err != nil {
			unix.Close(fds[0])
			unix.Close(fds[1])
			if i == 1 {
				unix.Close(p.fd[0])
			}
			return nil, fds, err
		}
		unix.CloseOnExec(d)
		p.fd[i] = d
	}
	return p, fds, nil
}

// peerEnded reports whether the end opposite side sends no more.
func (p *seqPair) peerEnded(side int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ended[1-side]
}

// peerClosed reports whether the end opposite side is closed.
func (p *seqPair) peerClosed(side int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed[1-side]
}

// end marks side as sending no more and owes its peer the end of the stream.
func (p *seqPair) end(side int) {
	p.mu.Lock()
	first := !p.ended[side]
	p.ended[side] = true
	p.mu.Unlock()
	if first {
		p.deliverEnd(1 - side)
	}
}

// deliverEnd sends side the empty datagram that ends its stream, from its
// peer, or owes it while side's buffer is full.
func (p *seqPair) deliverEnd(side int) {
	_, err := unix.SendmsgN(p.fd[1-side], nil, nil, nil, unix.MSG_DONTWAIT)
	p.mu.Lock()
	p.pending[side] = err != nil
	p.mu.Unlock()
}

// received is told that side took a message (empty is its length 0): an end
// of the stream it took is sent again, so the next receive and a poll see it
// too, and one still owed is sent now that there may be room.
func (p *seqPair) received(side int, empty bool) {
	p.mu.Lock()
	again := p.ended[1-side] && (empty || p.pending[side])
	p.mu.Unlock()
	if again {
		p.deliverEnd(side)
	}
}

// release is the guest's last close of side; the host sockets go with the
// second end.
func (p *seqPair) release(side int) {
	p.mu.Lock()
	p.closed[side] = true
	both := p.closed[1-side]
	p.mu.Unlock()
	if both {
		unix.Close(p.fd[0])
		unix.Close(p.fd[1])
		return
	}
	p.end(side)
}
