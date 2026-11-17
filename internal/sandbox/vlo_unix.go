//go:build darwin || linux

package sandbox

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// The run's own loopback ("vlo"). A run with host networking has the host's
// network but, as a network namespace has, its own loopback: what its sockets
// send to 127.0.0.0/8, ::1 or 0.0.0.0 reaches only its own sockets, through
// vnet in the host namespace's scope, never a socket of the host.
//
// A socket starts as a host socket. The first bind, connect or send that names
// the loopback makes it a vnet socket (toVlo): its host fd gives way to a unix
// one, which the epolls watching it watch instead (rewatch). A socket bound to
// a wildcard is both: its host socket for the network, and a vnet one holding
// the same port on the loopback (vloClaim), which accept, receive and
// readiness take from as well, until a connect picks one of the two.

// vloIP classifies a socket address for the run's own loopback: loop for
// 127.0.0.0/8 (v4-mapped or not) or ::1, any for a wildcard; v6 for IPv6's
// own address, :: or ::1 (parseSock).
func vloIP(raw []byte) (ip uint32, port uint16, v6, loop, any bool) {
	ip, port, v6, ok := parseSock(raw)
	if !ok {
		return 0, 0, false, false, false
	}
	return ip, port, v6, ip>>24 == 127, ip == 0
}

// vloFile makes the unix socket that carries a socket of type typ on the run's
// own loopback, set up as newSockFile sets a socket up.
func vloFile(typ int) (*os.File, error) {
	fd, err := unix.Socket(unix.AF_UNIX, typ, 0)
	if err != nil {
		return nil, err
	}
	d := newSockFile(fd, lxAFUnix, typ, 0)
	if typ == lxSockStream {
		vnetStreamBufs(fd)
	}
	return d.f, nil
}

// vnetStreamBufs gives the unix stream behind a vnet TCP socket the room TCP
// over a loopback grows to, megabytes (Linux tunes tcp_wmem and tcp_rmem up
// as a connection needs): at a unix stream's default, Darwin's 8 KiB, a peer
// that writes back what it reads blocks one that sends a large message, and
// neither moves. The host caps the size at its maximum.
func vnetStreamBufs(fd int) {
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, 4<<20)
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 4<<20)
}

// hostPort is the port d's host socket is bound to, 0 if none.
func hostPort(d *dfile) uint16 {
	sa, err := unix.Getsockname(hostFd(d.f))
	if err != nil {
		return 0
	}
	switch a := sa.(type) {
	case *unix.SockaddrInet4:
		return uint16(a.Port)
	case *unix.SockaddrInet6:
		return uint16(a.Port)
	}
	return 0
}

// hostAny reports whether d's host socket is bound to a wildcard address.
func hostAny(d *dfile) bool {
	sa, err := unix.Getsockname(hostFd(d.f))
	if err != nil {
		return false
	}
	switch a := sa.(type) {
	case *unix.SockaddrInet4:
		return a.Port != 0 && a.Addr == [4]byte{}
	case *unix.SockaddrInet6:
		return a.Port != 0 && a.Addr == [16]byte{}
	}
	return false
}

// swapFile puts nf behind d in place of its host file, moving the epolls that
// watch d over before the old file closes.
func (d *dfile) swapFile(nf *os.File) {
	old := d.f
	d.f = nf
	d.rewatch()
	old.Close()
}

// rewatch moves d's epoll registrations to the host fds now behind it.
func (d *dfile) rewatch() {
	d.epw.mu.Lock()
	eps := make([]*epoll, 0, len(d.epw.eps))
	for ep := range d.epw.eps {
		eps = append(eps, ep)
	}
	d.epw.mu.Unlock()
	for _, ep := range eps {
		ep.rewatch(d)
	}
}

// vloFd is the host fd of d's side of the run's own loopback, -1 if it has
// none.
func vloFd(d *dfile) int {
	if d.sk == nil || d.sk.vlo == nil {
		return -1
	}
	return hostFd(d.sk.vlo)
}

// toVlo makes d, a host INET socket, a socket of the run's own loopback: a
// vnet socket in the host namespace's scope. The options set on its host side
// stay with it (keepOpts).
func (g *dguest) toVlo(d *dfile) lxErrno {
	nf, err := vloFile(d.sk.typ)
	if err != nil {
		return osErrno(err)
	}
	keepOpts(d.sk, hostFd(d.f))
	d.swapFile(nf)
	d.sk.virt, d.sk.netns = true, g.ps.netns.Load()
	return 0
}

// freshHost replaces d's host socket, bound to a port the run's own loopback
// holds, by an unbound one with the same options.
func freshHost(d *dfile) lxErrno {
	dw, _ := dwFamily(d.sk.family)
	fd, err := unix.Socket(dw, d.sk.typ, d.sk.proto)
	if err != nil {
		return osErrno(err)
	}
	copyOpts(hostFd(d.f), fd)
	d.swapFile(newSockFile(fd, d.sk.family, d.sk.typ, d.sk.proto).f)
	return 0
}

// vloBindAny binds d, a host INET socket, to the wildcard raw names, and its
// port on the run's own loopback too. A port the host chose that the loopback
// holds is chosen again; one the guest named is in use.
func (g *dguest) vloBindAny(d *dfile, raw []byte, port uint16) int64 {
	sa, e := inetSockaddr(raw, d.sk.family, false)
	if e != 0 {
		return sErr(e)
	}
	for range 16 {
		if err := unix.Bind(hostFd(d.f), sa); err != nil {
			return sErr(osErrno(err))
		}
		if g.vloClaim(d, hostPort(d)) == 0 {
			return 0
		}
		if e := freshHost(d); e != 0 {
			return sErr(e)
		}
		if port != 0 {
			return sErr(lxEADDRINUSE)
		}
	}
	return sErr(lxEADDRINUSE)
}

// vloClaim gives d, a host socket bound to a wildcard on port, its side of the
// run's own loopback: a vnet socket holding the same port there. The
// reservation is the socket's vnet one (vk), released with it.
func (g *dguest) vloClaim(d *dfile, port uint16) lxErrno {
	sk := d.sk
	n := g.netState()
	if old := sk.vlo; old != nil {
		// One released by a connect to the network, read no further: what it
		// still holds is dropped.
		sk.mu.Lock()
		sk.vlo = nil
		sk.mu.Unlock()
		d.rewatch()
		old.Close()
	}
	f, err := vloFile(sk.typ)
	if err != nil {
		return osErrno(err)
	}
	tab, name := n.vports, "vnet"
	if sk.typ != lxSockStream {
		tab, name = n.vudps, "vudp"
	}
	n.mu.Lock()
	k, vp, e := n.vreserve(tab, name, vkey{g.ps.netns.Load(), 0, port, sk.family == lxAFInet6}, hostV6only(d), g.hostShare(d), sk.typ == lxSockStream)
	n.mu.Unlock()
	if e != 0 {
		f.Close()
		return e
	}
	if err := n.bindUnix(hostFd(f), vp.path); err != nil {
		n.mu.Lock()
		vremove(tab, k, vp)
		n.mu.Unlock()
		f.Close()
		return osErrno(err)
	}
	sk.mu.Lock()
	sk.vlo = f
	sk.vk, sk.vp, sk.vbound, sk.vudp, sk.vn, sk.unlink = k, vp, true, sk.typ != lxSockStream, n, vp.path
	sk.mu.Unlock()
	d.rewatch()
	return 0
}

// hostShare is how d's host socket shares what it binds (SO_REUSEADDR,
// SO_REUSEPORT), as the guest's euid.
func (g *dguest) hostShare(d *dfile) vshare {
	h := hostFd(d.f)
	ra, _ := unix.GetsockoptInt(h, unix.SOL_SOCKET, unix.SO_REUSEADDR)
	rp, _ := unix.GetsockoptInt(h, unix.SOL_SOCKET, unix.SO_REUSEPORT)
	return vshare{reuse: ra != 0, reusePort: rp != 0, uid: g.ps.cred.euid}
}

// hostV6only reports whether d's host socket is an IPv6 one that binds IPv6's
// addresses alone (IPV6_V6ONLY).
func hostV6only(d *dfile) bool {
	if d.sk.family != lxAFInet6 {
		return false
	}
	v, err := unix.GetsockoptInt(hostFd(d.f), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY)
	return err == nil && v != 0
}

// vloRelease drops d's side of the run's own loopback, for a connect that
// picked the host's network: its reservation goes, so nothing more reaches it
// there, but a datagram socket still reads what was sent to it there before,
// as Linux keeps a connecting socket's queue.
func (d *dfile) vloRelease() {
	sk := d.sk
	sk.mu.Lock()
	f, bound := sk.vlo, sk.vbound
	n, k, vp, udp, path := sk.vn, sk.vk, sk.vp, sk.vudp, sk.unlink
	if !bound {
		sk.mu.Unlock()
		return
	}
	sk.vbound, sk.vn, sk.vp, sk.unlink = false, nil, nil, ""
	if !udp {
		sk.vlo = nil
	}
	sk.mu.Unlock()
	if !udp {
		d.rewatch()
		f.Close()
	}
	n.mu.Lock()
	if udp {
		vremove(n.vudps, k, vp)
	} else {
		vremove(n.vports, k, vp)
	}
	n.mu.Unlock()
	os.Remove(path)
}

// vloSide makes sure d, a host datagram socket about to send to the run's own
// loopback, has a side there: an unbound one is bound to a wildcard first, as
// the kernel binds it on its first send. It reports false for one bound to an
// address of the host's own, which sends nothing there.
func (g *dguest) vloSide(d *dfile) bool {
	if d.sk.vlo != nil && d.sk.vbound {
		return true
	}
	switch {
	case hostPort(d) == 0:
		any := vSockaddr(d.sk.family, 0, 0)
		return g.vloBindAny(d, any, 0) == 0
	case hostAny(d):
		return g.vloClaim(d, hostPort(d)) == 0
	}
	return false
}

// vloConnect connects d, a host INET socket, to raw on the run's own loopback,
// making it that loopback's socket. One bound to a wildcard becomes its side
// there (adoptVlo); one bound to an address of the host's reaches nothing
// there.
func (g *dguest) vloConnect(d *dfile, raw []byte) int64 {
	_, _, v6, _, _ := vloIP(raw)
	if !v6 && saFamily(raw) == lxAFInet6 && hostV6only(d) {
		return sErr(lxENETUNREACH) // an IPv4-mapped address, on a socket IPv6's alone
	}
	switch {
	case hostPort(d) == 0:
		if e := g.toVlo(d); e != 0 {
			return sErr(e)
		}
	case !hostAny(d):
		return sErr(lxECONNREFUSED)
	default:
		if !d.sk.vbound {
			if e := g.vloClaim(d, hostPort(d)); e != 0 { // released by an earlier connect
				return sErr(e)
			}
		}
		g.adoptVlo(d)
	}
	if vnetStream(d) {
		return g.vconnect(d, raw)
	}
	return g.virtConnect(d, raw)
}

// adoptVlo makes d's side of the run's own loopback d itself: its unix socket
// takes the host socket's place, keeping the port it holds there and what was
// sent to it there, with the options the host socket had (keepOpts). What the
// host socket holds unread is dropped: a datagram from the network has no
// address the unix socket can name it by. A Linux socket keeps both; a
// pending queue of such datagrams, served before the unix socket, would.
func (g *dguest) adoptVlo(d *dfile) {
	keepOpts(d.sk, hostFd(d.f))
	d.sk.mu.Lock()
	f := d.sk.vlo
	d.sk.vlo = nil
	d.sk.mu.Unlock()
	d.swapFile(f)
	d.sk.virt, d.sk.netns = true, g.ps.netns.Load()
}

// vloSend sends a datagram from d's side of the run's own loopback to
// dst:port there (IPv6's own address when v6); one to a port no socket of the
// run is bound on is dropped, as the kernel drops it.
func (g *dguest) vloSend(d *dfile, iovs [][2]uint64, dst uint32, port uint16, v6 bool) int64 {
	total := iovTotal(iovs)
	if total > 65507 {
		return sErr(lxEMSGSIZE)
	}
	data, e := g.gather(iovs, 0, total)
	if e != 0 {
		return sErr(e)
	}
	if dst == 0 {
		dst = 127<<24 | 1
	}
	n := g.netState()
	scope, keyIP, _ := n.scopeOf(g.ps.netns.Load(), dst)
	fd, src := g.vsrcFd(d, vloFd(d), dst, v6)
	n.mu.Lock()
	path := n.vudpTarget(scope, keyIP, port, v6, src)
	n.mu.Unlock()
	if path != "" {
		n.viaShort(path, func(sa *unix.SockaddrUnix) error { return unix.Sendto(fd, data, unix.MSG_DONTWAIT, sa) })
	}
	return int64(total)
}

// dualIO runs op on d's host socket, then on its side of the run's own
// loopback, until one answers other than EAGAIN. A blocking call waits for
// either, ended by a signal or the guest's teardown, as sockIO's are.
func (g *dguest) dualIO(d *dfile, nonblock bool, timeo time.Duration, op func(h int) error) error {
	var end time.Time
	if timeo > 0 {
		end = time.Now().Add(timeo)
	}
	for {
		vlo := d.sk.vlo
		for _, f := range []*os.File{d.f, vlo} {
			if f == nil {
				continue
			}
			rc, err := f.SyscallConn()
			if err != nil {
				return unix.EBADF
			}
			var opErr error
			if rc.Control(func(fd uintptr) { opErr = op(int(fd)) }) != nil {
				return unix.EBADF
			}
			if opErr != unix.EAGAIN {
				return opErr
			}
		}
		if nonblock || timeo < 0 || vlo == nil {
			return unix.EAGAIN
		}
		wait := time.Duration(-1)
		if !end.IsZero() {
			if wait = time.Until(end); wait <= 0 {
				return unix.EAGAIN
			}
		}
		reqs := []pollReq{{rfd: hostFd(d.f), wfd: -1, xfd: -1, in: true}, {rfd: hostFd(vlo), wfd: -1, xfd: -1, in: true}}
		if _, e := g.hostPoll(reqs, []bool{false, false}, wait, nil); e != 0 {
			return unix.Errno(e)
		}
	}
}

// vloAccepted is the socket an accept on d's side of the run's own loopback
// took: a vnet socket of the loopback, at the address the listener holds
// there, from the client's.
func (g *dguest) vloAccepted(d *dfile, nfd int) *dfile {
	vnetStreamBufs(nfd)
	nd := newSockFile(nfd, d.sk.family, lxSockStream, d.sk.proto)
	nd.sk.virt, nd.sk.netns = true, g.ps.netns.Load()
	c, dst := vkey{ip: 127<<24 | 1, port: d.sk.vk.port}, vkey{ip: 127<<24 | 1, port: d.sk.vk.port}
	if src, to, ok := g.netState().vpeer(nfd, nd.sk.netns); ok {
		c = src
		if to.port != 0 {
			dst = to
		} else {
			dst.v6 = c.v6
		}
	}
	nd.sk.name = vName(d.sk.family, dst.ip, dst.port, dst.v6)
	nd.sk.peer = vName(d.sk.family, c.ip, c.port, c.v6)
	return nd
}

// vloKept reports whether an option is one a socket of the run's own loopback
// keeps for itself: its unix socket has no TCP or IP layer, and its ports are
// vnet's. They read back as set (keepOpts carries them over from the host
// side).
func vloKept(level, name int) bool {
	if level != lxSolSocket {
		return true
	}
	switch name {
	case 2, 5, 6, 9, 10, 15: // SO_REUSEADDR SO_DONTROUTE SO_BROADCAST SO_KEEPALIVE SO_OOBINLINE SO_REUSEPORT
		return true
	}
	return false
}

// keepOpts records in sk the options of host socket h a socket of the run's
// own loopback keeps for itself, as h has them: those h has set, its value
// differing from a fresh host socket's. One left unset reads as Linux's
// default (keptDefault), not as the host's.
func keepOpts(sk *dsock, h int) {
	dw, _ := dwFamily(sk.family)
	fresh, ferr := unix.Socket(dw, sk.typ, 0)
	if ferr == nil {
		defer unix.Close(fresh)
	}
	read := func(fd int, om optMap) (int, error) {
		if om.kind == optByte {
			b, err := unix.GetsockoptByte(fd, om.level, om.name)
			return int(b), err
		}
		return unix.GetsockoptInt(fd, om.level, om.name)
	}
	sk.mu.Lock()
	defer sk.mu.Unlock()
	if sk.opts == nil {
		sk.opts = map[[2]int]int32{}
	}
	for k, om := range sockopts {
		if !vloKept(k[0], k[1]) || om.kind == optNoop || om.kind == optRecv {
			continue
		}
		if _, set := sk.opts[k]; set {
			continue
		}
		v, err := read(h, om)
		if err != nil {
			continue
		}
		if ferr == nil {
			if d, err := read(fresh, om); err == nil && d == v {
				continue
			}
		}
		if v != 0 && (k[0] == lxSolSocket || k == [2]int{lxSolTCP, 1}) {
			v = 1 // a boolean the host reports as its flag bit
		}
		sk.opts[k] = int32(v)
	}
}

// copyOpts sets on host socket to the options host socket from has.
func copyOpts(from, to int) {
	for _, om := range sockopts {
		if om.kind != optInt {
			continue
		}
		if v, err := unix.GetsockoptInt(from, om.level, om.name); err == nil {
			unix.SetsockoptInt(to, om.level, om.name, v)
		}
	}
}
