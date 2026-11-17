//go:build darwin

package sandbox

import "golang.org/x/sys/unix"

// Socket refinements to ppoll and pselect6 (fdpoll_darwin.go): Darwin and Linux
// disagree on when a stream socket has hung up.

// sockPollState reads a stream socket's shutdown state the way Linux's poll
// reports it, for when Darwin's flags cannot say: Darwin raises POLLHUP as
// soon as either direction ends and then hides POLLOUT, while Linux reports
// HUP only once both have (or the socket is unconnected) and keeps OUT. hup
// tells that Darwin reported a hang-up. ok is false when the state is not one
// of those (connecting, listening, not a stream socket).
func sockPollState(d *dfile, hup bool) (uint16, bool) {
	if d == nil || d.sk == nil || d.sk.typ != lxSockStream {
		return 0, false
	}
	d.sk.mu.Lock()
	listening := d.sk.listening
	d.sk.mu.Unlock()
	if listening {
		return 0, false
	}
	if d.sk.virt {
		return vnetSockState(d, hup)
	}
	h := hostFd(d.f)
	_, err := unix.Getpeername(h)
	switch err {
	case unix.EINVAL:
		// Darwin refuses getpeername once both directions are shut down.
		return lxPollHup | sockIn | sockOut, true
	case unix.ENOTCONN:
		if d.sk.inet() {
			if !tcpClosed(h) { // still connecting
				return 0, false
			}
		}
		return lxPollHup | sockOut, true
	case nil:
		if !hup {
			return 0, false
		}
		st := uint16(sockOut)
		var b [1]byte
		if _, _, err := unix.Recvfrom(h, b[:], unix.MSG_PEEK|unix.MSG_DONTWAIT); err != unix.EAGAIN {
			st |= sockIn
		}
		return st, true
	}
	return 0, false
}

const (
	sockIn  = lxPollIn | lxPollRdnorm | lxPollRdhup
	sockOut = lxPollOut | lxPollWrnorm
)

// vnetSockState is sockPollState for a vnet TCP socket, whose unix stream
// keeps a peer name after it disconnects, so the state comes from the socket
// itself. Unconnected, it has hung up, as TCP's has. Connected, it reads its
// end once the stream does (eof: its peer shut down writing or closed, TCP's
// FIN), stays writable, and has hung up once its own write side is shut too.
func vnetSockState(d *dfile, eof bool) (uint16, bool) {
	d.sk.mu.Lock()
	connected, wrShut := d.sk.peer != nil, d.sk.wrShut
	d.sk.mu.Unlock()
	switch {
	case !connected:
		return lxPollHup | sockOut, true
	case !eof:
		return 0, false
	case wrShut:
		return lxPollHup | sockIn | sockOut, true
	}
	return sockIn | sockOut, true
}

// peekEOF reports whether stream socket h reads its end now: nothing left
// before it.
func peekEOF(h int) bool {
	var b [1]byte
	n, _, err := unix.Recvfrom(h, b[:], unix.MSG_PEEK|unix.MSG_DONTWAIT)
	return err == nil && n == 0
}
