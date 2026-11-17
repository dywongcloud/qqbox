//go:build darwin || linux

package sandbox

import (
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// A netlink socket (lx_netlink.go) here is one end of a host datagram
// socketpair; a request's reply datagrams are written into the other end
// before send returns, so recv, MSG_DONTWAIT and poll see them as on Linux.

type netlinkState struct {
	peer    int         // host end the replies are written into
	proto   int         // NETLINK_ROUTE (0) or NETLINK_NETFILTER
	overrun atomic.Bool // replies were dropped: the next receive fails with ENOBUFS
	dump    *nlDump
}

func (g *dguest) netlinkSocket(typ, proto, flags int) int64 {
	if typ != unix.SOCK_RAW && typ != unix.SOCK_DGRAM {
		return sErr(lxESOCKTNOSUPPORT)
	}
	if proto != 0 && proto != netlinkNetfilter { // NETLINK_ROUTE and NETLINK_NETFILTER
		return sErr(lxEPROTONOSUPPORT)
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		return sErr(osErrno(err))
	}
	unix.CloseOnExec(fds[1])
	unix.SetsockoptInt(fds[1], unix.SOL_SOCKET, unix.SO_SNDBUF, 256<<10)
	unix.SetNonblock(fds[1], true)
	d := newSockFile(fds[0], lxAFNetlink, typ, proto)
	unix.SetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_RCVBUF, 1<<20)
	d.sk.nl = &netlinkState{peer: fds[1], proto: proto, dump: newNlDump(g.guestPageSize())}
	d.sk.netns = g.ps.netns.Load() // a netlink socket works in the netns it was created in
	return g.installSock(d, flags)
}

// netlinkSend consumes the guest's netlink requests and queues the replies.
func (g *dguest) netlinkSend(d *dfile, iovs [][2]uint64) int64 {
	total := iovTotal(iovs)
	req, e := g.gather(iovs, 0, min(total, 65536))
	if e != 0 {
		return sErr(e)
	}
	reply := g.nlReplies
	if d.sk.nl.proto == netlinkNetfilter {
		reply = g.nftReplies
	}
	for _, dg := range d.sk.nl.dump.queue(reply(req, d.sk.netns), uint32(g.tgid)) {
		if !d.sk.nl.put(dg) {
			break
		}
	}
	return int64(total)
}

// netlinkReceived follows a receive with an n-byte buffer: the running dump,
// if any, queues its next datagram.
func (g *dguest) netlinkReceived(d *dfile, n int) {
	if dg := d.sk.nl.dump.received(n); dg != nil {
		d.sk.nl.put(dg)
	}
}

// put queues a reply datagram. A full receive queue drops it, as on Linux,
// which reports the overrun on the next receive.
func (nl *netlinkState) put(dg []byte) bool {
	if _, err := unix.Write(nl.peer, dg); err != nil {
		nl.overrun.Store(true)
		return false
	}
	return true
}
