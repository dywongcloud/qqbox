//go:build darwin || linux

package sandbox

import (
	"time"

	"golang.org/x/sys/unix"
)

// Socket options: the Linux (level, name) pairs the guest uses, mapped to
// Darwin's, with value layouts converted where they differ. Linux-only
// options that only tune behaviour are accepted and remembered so getsockopt
// reads back what was set.

// optKind is how an option's value is laid out.
type optKind int

const (
	optInt  optKind = iota // int on both sides
	optByte                // int (or byte) for the guest, u_char on Darwin
	optNoop                // Linux-only: remembered, not applied
	optRecv                // a receive-ancillary switch: remembered, and applied where the host takes it
)

type optMap struct {
	level, name int
	kind        optKind
}

// sockopts maps Linux {level, name} to Darwin's.
var sockopts = map[[2]int]optMap{
	{lxSolSocket, 1}:  {unix.SOL_SOCKET, unix.SO_DEBUG, optInt},
	{lxSolSocket, 2}:  {unix.SOL_SOCKET, unix.SO_REUSEADDR, optInt},
	{lxSolSocket, 5}:  {unix.SOL_SOCKET, unix.SO_DONTROUTE, optInt},
	{lxSolSocket, 6}:  {unix.SOL_SOCKET, unix.SO_BROADCAST, optInt},
	{lxSolSocket, 7}:  {unix.SOL_SOCKET, unix.SO_SNDBUF, optInt},
	{lxSolSocket, 8}:  {unix.SOL_SOCKET, unix.SO_RCVBUF, optInt},
	{lxSolSocket, 9}:  {unix.SOL_SOCKET, unix.SO_KEEPALIVE, optInt},
	{lxSolSocket, 10}: {unix.SOL_SOCKET, unix.SO_OOBINLINE, optInt},
	{lxSolSocket, 11}: {0, 0, optNoop}, // SO_NO_CHECK
	{lxSolSocket, 12}: {0, 0, optNoop}, // SO_PRIORITY
	{lxSolSocket, 14}: {0, 0, optNoop}, // SO_BSDCOMPAT
	{lxSolSocket, 15}: {unix.SOL_SOCKET, unix.SO_REUSEPORT, optInt},
	{lxSolSocket, 16}: {0, 0, optNoop}, // SO_PASSCRED
	{lxSolSocket, 18}: {unix.SOL_SOCKET, unix.SO_RCVLOWAT, optInt},
	{lxSolSocket, 19}: {unix.SOL_SOCKET, unix.SO_SNDLOWAT, optInt},
	{lxSolSocket, 25}: {0, 0, optNoop},                           // SO_BINDTODEVICE
	{lxSolSocket, 29}: {0, 0, optNoop},                           // SO_TIMESTAMP
	{lxSolSocket, 32}: {unix.SOL_SOCKET, unix.SO_SNDBUF, optInt}, // SO_SNDBUFFORCE
	{lxSolSocket, 33}: {unix.SOL_SOCKET, unix.SO_RCVBUF, optInt}, // SO_RCVBUFFORCE
	{lxSolSocket, 34}: {0, 0, optNoop},                           // SO_PASSSEC
	{lxSolSocket, 36}: {0, 0, optNoop},                           // SO_MARK
	{lxSolSocket, 40}: {0, 0, optNoop},                           // SO_RXQ_OVFL
	{lxSolSocket, 46}: {0, 0, optNoop},                           // SO_BUSY_POLL
	{lxSolSocket, 49}: {0, 0, optNoop},                           // SO_INCOMING_CPU
	{lxSolSocket, 60}: {0, 0, optNoop},                           // SO_ZEROCOPY

	{lxSolTCP, 1}:  {unix.IPPROTO_TCP, unix.TCP_NODELAY, optInt},
	{lxSolTCP, 2}:  {unix.IPPROTO_TCP, unix.TCP_MAXSEG, optInt},
	{lxSolTCP, 3}:  {0, 0, optNoop},                                    // TCP_CORK
	{lxSolTCP, 4}:  {unix.IPPROTO_TCP, hostTCPKeepIdle, optInt},        // TCP_KEEPIDLE
	{lxSolTCP, 5}:  {unix.IPPROTO_TCP, unix.TCP_KEEPINTVL, optInt},     // TCP_KEEPINTVL
	{lxSolTCP, 6}:  {unix.IPPROTO_TCP, unix.TCP_KEEPCNT, optInt},       // TCP_KEEPCNT
	{lxSolTCP, 7}:  {0, 0, optNoop},                                    // TCP_SYNCNT
	{lxSolTCP, 8}:  {0, 0, optNoop},                                    // TCP_LINGER2
	{lxSolTCP, 9}:  {0, 0, optNoop},                                    // TCP_DEFER_ACCEPT
	{lxSolTCP, 10}: {0, 0, optNoop},                                    // TCP_WINDOW_CLAMP
	{lxSolTCP, 12}: {0, 0, optNoop},                                    // TCP_QUICKACK
	{lxSolTCP, 18}: {0, 0, optNoop},                                    // TCP_USER_TIMEOUT
	{lxSolTCP, 23}: {0, 0, optNoop},                                    // TCP_FASTOPEN
	{lxSolTCP, 25}: {unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, optInt}, // TCP_NOTSENT_LOWAT
	{lxSolTCP, 30}: {0, 0, optNoop},                                    // TCP_FASTOPEN_CONNECT

	{lxSolIP, 1}:  {unix.IPPROTO_IP, unix.IP_TOS, optInt},
	{lxSolIP, 2}:  {unix.IPPROTO_IP, unix.IP_TTL, optInt},
	{lxSolIP, 3}:  {unix.IPPROTO_IP, unix.IP_HDRINCL, optInt},
	{lxSolIP, 8}:  {unix.IPPROTO_IP, hostIPRecvPktinfo, optRecv}, // IP_PKTINFO
	{lxSolIP, 10}: {0, 0, optNoop},                               // IP_MTU_DISCOVER
	{lxSolIP, 11}: {0, 0, optNoop},                               // IP_RECVERR
	{lxSolIP, 12}: {unix.IPPROTO_IP, unix.IP_RECVTTL, optRecv},
	{lxSolIP, 13}: {unix.IPPROTO_IP, unix.IP_RECVTOS, optRecv},
	{lxSolIP, 15}: {0, 0, optNoop}, // IP_FREEBIND
	{lxSolIP, 19}: {0, 0, optNoop}, // IP_TRANSPARENT
	{lxSolIP, 24}: {0, 0, optNoop}, // IP_BIND_ADDRESS_NO_PORT
	{lxSolIP, 33}: {unix.IPPROTO_IP, unix.IP_MULTICAST_TTL, optByte},
	{lxSolIP, 34}: {unix.IPPROTO_IP, unix.IP_MULTICAST_LOOP, optByte},
	{lxSolIP, 49}: {0, 0, optNoop}, // IP_MULTICAST_ALL

	{lxSolIPv6, 16}: {unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, optInt},
	{lxSolIPv6, 17}: {unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF, optInt},
	{lxSolIPv6, 18}: {unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_HOPS, optInt},
	{lxSolIPv6, 19}: {unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_LOOP, optInt},
	{lxSolIPv6, 23}: {0, 0, optNoop}, // IPV6_MTU_DISCOVER
	{lxSolIPv6, 25}: {0, 0, optNoop}, // IPV6_RECVERR
	{lxSolIPv6, 26}: {unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, optInt},
	{lxSolIPv6, 29}: {0, 0, optNoop}, // IPV6_MULTICAST_ALL
	{lxSolIPv6, 49}: {unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO, optRecv},
	{lxSolIPv6, 51}: {unix.IPPROTO_IPV6, unix.IPV6_RECVHOPLIMIT, optRecv},
	{lxSolIPv6, 66}: {unix.IPPROTO_IPV6, unix.IPV6_RECVTCLASS, optRecv},
	{lxSolIPv6, 67}: {unix.IPPROTO_IPV6, unix.IPV6_TCLASS, optInt},
}

func (g *dguest) setsockopt(fd int64, level, name int, val uint64, vlen int64) int64 {
	d, e := g.sock(fd)
	if e != 0 {
		return sErr(e)
	}
	if vlen < 0 || vlen > 256 {
		return sErr(lxEINVAL)
	}
	b, err := g.argRead(val, int(vlen))
	if err != nil {
		return sErr(lxEFAULT)
	}
	sk := d.sk
	h := hostFd(d.f)
	if sk.nl != nil && level == lxSolNetlink {
		return 0
	}
	if level == lxSolSocket {
		switch name {
		case lxSoRcvtimeo, lxSoSndtimeo, lxSoRcvtimeoN, lxSoSndtimeoN:
			if vlen < 16 {
				return sErr(lxEINVAL)
			}
			sec, usec := int64(le64(b)), int64(le64(b[8:]))
			if usec < 0 || usec >= 1000000 {
				return sErr(lxEDOM)
			}
			t := time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond
			if sec < 0 {
				t = -1 // Linux: a zero timeout, so the call never waits
			}
			sk.mu.Lock()
			if name == lxSoRcvtimeo || name == lxSoRcvtimeoN {
				sk.rcvTimeo = t
			} else {
				sk.sndTimeo = t
			}
			sk.mu.Unlock()
			return 0
		case lxSoLinger:
			if vlen < 8 {
				return sErr(lxEINVAL)
			}
			l := struct{ onoff, linger int32 }{int32(le32(b)), int32(le32(b[4:]))}
			if err := unix.SetsockoptLinger(h, unix.SOL_SOCKET, hostSoLinger, &unix.Linger{Onoff: l.onoff, Linger: l.linger}); err != nil {
				return sErr(osErrno(err))
			}
			return 0
		}
	}
	if sk.virt && sk.inet() {
		switch {
		case level == lxSolIP && (name == lxIPAddMember || name == lxIPDropMember):
			return g.vmcMembership(d, b, name == lxIPDropMember)
		case level == lxSolIP && name == lxIPMulticastIf:
			return g.vmcSetIf(d, b)
		case level == lxSolIPv6 && (name == 20 || name == 21) && sk.family == lxAFInet6:
			return g.vmcMembership6(d, b, name == 21)
		case level == lxSolIPv6 && name == 17:
			return g.vmcSetIf6(d, b)
		}
	}
	if level == lxSolIP && sk.inet() {
		switch name {
		case lxIPAddMember, lxIPDropMember:
			if vlen < 8 {
				return sErr(lxEINVAL)
			}
			// ip_mreq, or ip_mreqn whose address field sits in the same place.
			m := &unix.IPMreq{}
			copy(m.Multiaddr[:], b[0:4])
			copy(m.Interface[:], b[4:8])
			opt := unix.IP_ADD_MEMBERSHIP
			if name == lxIPDropMember {
				opt = unix.IP_DROP_MEMBERSHIP
			}
			if err := unix.SetsockoptIPMreq(h, unix.IPPROTO_IP, opt, m); err != nil {
				return sErr(osErrno(err))
			}
			return 0
		case lxIPMulticastIf:
			if vlen < 4 {
				return sErr(lxEINVAL)
			}
			a := b[0:4]
			if vlen >= 8 {
				a = b[4:8] // ip_mreqn.imr_address
			}
			var ip [4]byte
			copy(ip[:], a)
			if err := unix.SetsockoptInet4Addr(h, unix.IPPROTO_IP, unix.IP_MULTICAST_IF, ip); err != nil {
				return sErr(osErrno(err))
			}
			return 0
		}
	}
	if level == lxSolIPv6 && (name == 20 || name == 21) && sk.family == lxAFInet6 {
		if vlen < 20 {
			return sErr(lxEINVAL)
		}
		m := &unix.IPv6Mreq{Interface: le32(b[16:])}
		copy(m.Multiaddr[:], b[:16])
		opt := unix.IPV6_JOIN_GROUP
		if name == 21 {
			opt = unix.IPV6_LEAVE_GROUP
		}
		if err := unix.SetsockoptIPv6Mreq(h, unix.IPPROTO_IPV6, opt, m); err != nil {
			return sErr(osErrno(err))
		}
		return 0
	}
	om, ok := sockopts[[2]int{level, name}]
	if !ok || (level != lxSolSocket && level != lxSolTCP && !sk.inet()) {
		return sErr(lxENOPROTOOPT)
	}
	if sk.virt && sk.inet() && vloKept(level, name) {
		om.kind = optNoop // a vnet socket keeps it itself: its unix socket has no such layer
	}
	var v int32
	switch {
	case vlen >= 4:
		v = int32(le32(b))
	case vlen >= 1 && level != lxSolSocket:
		v = int32(b[0])
	default:
		return sErr(lxEINVAL)
	}
	switch om.kind {
	case optNoop, optRecv:
		if om.kind == optRecv {
			unix.SetsockoptInt(h, om.level, om.name, int(v))
		}
		sk.mu.Lock()
		if sk.opts == nil {
			sk.opts = map[[2]int]int32{}
		}
		sk.opts[[2]int{level, name}] = v
		vp := sk.vp
		sk.mu.Unlock()
		if level == lxSolIP && name == lxIPMulticastAll && sk.virt && vp != nil {
			n := g.netState()
			n.mu.Lock()
			vp.mcNoAll = v == 0
			n.mu.Unlock()
		}
		return 0
	case optByte:
		err = unix.SetsockoptByte(h, om.level, om.name, byte(v))
	default:
		err = unix.SetsockoptInt(h, om.level, om.name, int(v))
	}
	if err != nil {
		return sErr(osErrno(err))
	}
	return 0
}

func (g *dguest) getsockopt(fd int64, level, name int, val, lenAddr uint64) int64 {
	d, e := g.sock(fd)
	if e != 0 {
		return sErr(e)
	}
	lb, err := g.argRead(lenAddr, 4)
	if err != nil {
		return sErr(lxEFAULT)
	}
	room := int32(le32(lb))
	if room < 0 {
		return sErr(lxEINVAL)
	}
	out, e := g.sockoptValue(d, level, name)
	if e != 0 {
		return sErr(e)
	}
	n := min(int(room), len(out))
	if n > 0 {
		if err := g.writeMem(val, out[:n]); err != nil {
			return sErr(lxEFAULT)
		}
	}
	put32(lb, uint32(n))
	if err := g.writeMem(lenAddr, lb); err != nil {
		return sErr(lxEFAULT)
	}
	return 0
}

// sockoptValue returns an option's current value in the guest's layout.
func (g *dguest) sockoptValue(d *dfile, level, name int) ([]byte, lxErrno) {
	sk := d.sk
	h := hostFd(d.f)
	if sk.nl != nil && level == lxSolNetlink {
		return i32bytes(0), 0
	}
	if level == lxSolSocket {
		switch name {
		case lxSoType:
			return i32bytes(sk.typ), 0
		case lxSoDomain:
			return i32bytes(sk.family), 0
		case lxSoProtocol:
			return i32bytes(sk.proto), 0
		case lxSoAcceptcon:
			sk.mu.Lock()
			defer sk.mu.Unlock()
			if sk.listening {
				return i32bytes(1), 0
			}
			return i32bytes(0), 0
		case lxSoError:
			v, err := unix.GetsockoptInt(h, unix.SOL_SOCKET, unix.SO_ERROR)
			if err != nil {
				return nil, osErrno(err)
			}
			if v != 0 {
				v = int(hostErrno(unix.Errno(v)))
			}
			return i32bytes(v), 0
		case lxSoRcvtimeo, lxSoRcvtimeoN, lxSoSndtimeo, lxSoSndtimeoN:
			sk.mu.Lock()
			defer sk.mu.Unlock()
			if name == lxSoRcvtimeo || name == lxSoRcvtimeoN {
				return timevalBytes(max(sk.rcvTimeo, 0)), 0
			}
			return timevalBytes(max(sk.sndTimeo, 0)), 0
		case lxSoLinger:
			l, err := unix.GetsockoptLinger(h, unix.SOL_SOCKET, hostSoLinger)
			if err != nil {
				return nil, osErrno(err)
			}
			var b [8]byte
			put32(b[:], uint32(l.Onoff))
			put32(b[4:], uint32(l.Linger))
			return b[:], 0
		case lxSoPeercred:
			if sk.family != lxAFUnix {
				return nil, lxENOPROTOOPT
			}
			// struct ucred of the connected peer: tracked for socketpairs and
			// connected pairs, else the caller stands in.
			c := ucred{pid: uint32(g.tgid), uid: g.ps.cred.euid, gid: g.ps.cred.egid}
			if sk.cl != nil {
				c = sk.cl.peer(sk.side)
			}
			// The link holds global pids; report the peer in the caller's namespace.
			if c.pid != 0 {
				c.pid = uint32(g.nsPid(int(c.pid)))
			}
			return c.bytes(), 0
		}
	}
	if level == lxSolIP && name == lxIPMulticastIf && sk.inet() {
		var a [4]byte
		if sk.virt {
			sk.mu.Lock()
			a = [4]byte{byte(sk.mcIfAddr >> 24), byte(sk.mcIfAddr >> 16), byte(sk.mcIfAddr >> 8), byte(sk.mcIfAddr)}
			sk.mu.Unlock()
		} else if v, err := unix.GetsockoptInet4Addr(h, unix.IPPROTO_IP, unix.IP_MULTICAST_IF); err == nil {
			a = v
		}
		return a[:], 0
	}
	om, ok := sockopts[[2]int{level, name}]
	if !ok || (level != lxSolSocket && level != lxSolTCP && !sk.inet()) {
		return nil, lxENOPROTOOPT
	}
	if sk.virt && sk.inet() && vloKept(level, name) {
		// A vnet socket keeps it itself (vlo_unix.go): what was set, else
		// Linux's default.
		return i32bytes(int(optOr(sk, level, name, keptDefault[[2]int{level, name}]))), 0
	}
	switch om.kind {
	case optNoop, optRecv:
		return i32bytes(int(optOr(sk, level, name, keptDefault[[2]int{level, name}]))), 0
	case optByte:
		v, err := unix.GetsockoptByte(h, om.level, om.name)
		if err != nil {
			return nil, osErrno(err)
		}
		return i32bytes(int(v)), 0
	}
	v, err := unix.GetsockoptInt(h, om.level, om.name)
	if err != nil {
		return nil, osErrno(err)
	}
	if v != 0 && (level == lxSolSocket && (name == 2 || name == 5 || name == 6 || name == 9 || name == 10 || name == 15) || level == lxSolTCP && name == 1) {
		v = 1 // Darwin reports a boolean option as its flag bit
	}
	return i32bytes(v), 0
}
