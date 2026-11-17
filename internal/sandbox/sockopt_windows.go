//go:build windows

package sandbox

import (
	"net"
	"time"
)

// Socket options on Windows. The ones with an effect here (timeouts, linger,
// IPV6_V6ONLY, TCP_NODELAY, SO_KEEPALIVE) are applied; the rest of the
// options Linux accepts are remembered so getsockopt reads back what was
// set. Read-only ones (SO_TYPE, SO_ERROR, SO_PEERCRED, ...) are computed.

// sockoptKnown lists the settable options per level, by Linux name.
var sockoptKnown = map[int]map[int]bool{
	lxSolSocket: {1: true, 2: true, 5: true, 6: true, 7: true, 8: true, 9: true, 10: true, 11: true, 12: true,
		13: true, 14: true, 15: true, 16: true, 18: true, 19: true, 20: true, 21: true, 25: true, 29: true,
		32: true, 33: true, 34: true, 36: true, 40: true, 46: true, 49: true, 60: true, 66: true, 67: true},
	lxSolTCP:  {1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true, 10: true, 12: true, 18: true, 23: true, 25: true, 30: true},
	lxSolIP:   {1: true, 2: true, 3: true, 8: true, 10: true, 11: true, 12: true, 13: true, 15: true, 19: true, 24: true, 32: true, 33: true, 34: true, 35: true, 36: true, 49: true},
	lxSolIPv6: {16: true, 17: true, 18: true, 19: true, 20: true, 21: true, 23: true, 25: true, 26: true, 29: true, 49: true, 51: true, 66: true, 67: true},
}

const (
	lxSoKeepalive = 9
	lxSoRcvbuf    = 8
	lxSoSndbuf    = 7
	lxTCPNodelay  = 1
)

func (g *dguest) setsockopt(fd int64, level, name int, val uint64, vlen int64) int64 {
	d, e := g.sock(fd)
	if e != 0 {
		return sErr(e)
	}
	s := d.sk
	if s.family == lxAFNetlink && level == lxSolNetlink {
		return 0 // the membership and reporting options change nothing here
	}
	if !sockoptKnown[level][name] {
		return sErr(lxENOPROTOOPT)
	}
	switch {
	case level == lxSolTCP && !(s.inet() && s.typ == lxSockStream):
		return sErr(lxEOPNOTSUPP)
	case (level == lxSolIP || level == lxSolIPv6) && !s.inet():
		return sErr(lxEOPNOTSUPP)
	case level == lxSolIPv6 && s.family != lxAFInet6:
		return sErr(lxENOPROTOOPT)
	}
	if vlen < 0 || vlen > 256 {
		return sErr(lxEINVAL)
	}
	b, err := g.readMem(val, int(vlen))
	if err != nil {
		return sErr(lxEFAULT)
	}
	if s.loop && s.inet() {
		switch {
		case level == lxSolIP && (name == lxIPAddMember || name == lxIPDropMember):
			return g.loopMembership(s, b, name == lxIPDropMember)
		case level == lxSolIP && name == lxIPMulticastIf:
			return g.loopSetIf(s, b)
		case level == lxSolIPv6 && (name == 20 || name == 21):
			return g.loopMembership6(s, b, name == 21)
		case level == lxSolIPv6 && name == 17:
			return g.loopSetIf6(s, b)
		}
	}
	var v int32
	if len(b) >= 4 {
		v = int32(le32(b))
	} else if len(b) >= 1 && level != lxSolSocket {
		v = int32(b[0])
	} else if !(level == lxSolSocket && (name == 25 || name == lxSoLinger)) {
		return sErr(lxEINVAL)
	}
	n := s.n
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case level == lxSolSocket && (name == lxSoRcvtimeo || name == lxSoSndtimeo || name == lxSoRcvtimeoN || name == lxSoSndtimeoN):
		if len(b) < 16 {
			return sErr(lxEINVAL)
		}
		sec, usec := int64(le64(b)), int64(le64(b[8:]))
		if usec < 0 || usec >= 1e6 {
			return sErr(lxEDOM)
		}
		t := time.Duration(max(sec, 0))*time.Second + time.Duration(usec)*time.Microsecond
		if name == lxSoRcvtimeo || name == lxSoRcvtimeoN {
			s.rcvTimeo = t
		} else {
			s.sndTimeo = t
		}
	case level == lxSolSocket && name == lxSoLinger:
		if len(b) < 8 {
			return sErr(lxEINVAL)
		}
		s.linger0 = le32(b) != 0 && le32(b[4:]) == 0
	case level == lxSolIPv6 && name == lxIPv6V6only:
		if s.ln != nil || s.pc != nil || s.conn != nil || s.loop && s.laddr.IsValid() {
			return sErr(lxEINVAL)
		}
		s.v6only = v != 0
	case level == lxSolTCP && name == lxTCPNodelay:
		if tc, ok := s.conn.(*net.TCPConn); ok {
			tc.SetNoDelay(v != 0)
		}
	case level == lxSolSocket && name == lxSoKeepalive:
		if tc, ok := s.conn.(*net.TCPConn); ok {
			tc.SetKeepAlive(v != 0)
		}
	}
	if s.opts == nil {
		s.opts = map[[2]int][]byte{}
	}
	s.opts[[2]int{level, name}] = append([]byte(nil), b...)
	return 0
}

func (g *dguest) getsockopt(fd int64, level, name int, val, lenAddr uint64) int64 {
	d, e := g.sock(fd)
	if e != 0 {
		return sErr(e)
	}
	lb, err := g.readMem(lenAddr, 4)
	if err != nil {
		return sErr(lxEFAULT)
	}
	room := int32(le32(lb))
	if room < 0 {
		return sErr(lxEINVAL)
	}
	out, e := sockoptValue(d.sk, level, name)
	if d.sk.family == lxAFNetlink && level == lxSolNetlink {
		out, e = i32bytes(0), 0
	}
	if e != 0 {
		return sErr(e)
	}
	k := min(int(room), len(out))
	if k > 0 {
		if err := g.writeMem(val, out[:k]); err != nil {
			return sErr(lxEFAULT)
		}
	}
	put32(lb, uint32(k))
	if err := g.writeMem(lenAddr, lb); err != nil {
		return sErr(lxEFAULT)
	}
	return 0
}

// sockoptValue is an option's current value in the guest's layout.
func sockoptValue(s *dsock, level, name int) ([]byte, lxErrno) {
	n := s.n
	n.mu.Lock()
	defer n.mu.Unlock()
	if level == lxSolSocket {
		switch name {
		case lxSoType:
			return i32bytes(s.typ), 0
		case lxSoDomain:
			return i32bytes(s.family), 0
		case lxSoProtocol:
			return i32bytes(s.proto), 0
		case lxSoAcceptcon:
			return i32bytes(int(b2i(s.state == skListening))), 0
		case lxSoError:
			e := s.soErr
			s.soErr = 0
			return i32bytes(int(e)), 0
		case lxSoPeercred:
			c := s.peerCred
			if s.family != lxAFUnix || s.state != skConnected {
				c = ucred{uid: ^uint32(0), gid: ^uint32(0)}
			}
			var b [12]byte
			put32(b[:], c.pid)
			put32(b[4:], c.uid)
			put32(b[8:], c.gid)
			return b[:], 0
		case lxSoRcvtimeo, lxSoRcvtimeoN:
			return timevalBytes(s.rcvTimeo), 0
		case lxSoSndtimeo, lxSoSndtimeoN:
			return timevalBytes(s.sndTimeo), 0
		case lxSoLinger:
			if v, ok := s.opts[[2]int{level, name}]; ok && len(v) >= 8 {
				return v[:8], 0
			}
			return make([]byte, 8), 0
		case lxSoRcvbuf, lxSoSndbuf:
			if v, ok := s.opts[[2]int{level, name}]; ok && len(v) >= 4 {
				return i32bytes(2 * int(int32(le32(v)))), 0
			}
			return i32bytes(sockRcvBuf), 0
		}
	}
	switch {
	case level == lxSolTCP && !(s.inet() && s.typ == lxSockStream):
		return nil, lxEOPNOTSUPP
	case (level == lxSolIP || level == lxSolIPv6) && !s.inet():
		return nil, lxEOPNOTSUPP
	}
	if level == lxSolIPv6 && name == lxIPv6V6only {
		return i32bytes(int(b2i(s.v6only))), 0
	}
	if level == lxSolTCP && name == 2 { // TCP_MAXSEG
		return i32bytes(1460), 0
	}
	if !sockoptKnown[level][name] {
		return nil, lxENOPROTOOPT
	}
	if level == lxSolIP && name == lxIPMulticastIf && s.loop {
		a := s.mcIfAddr
		return []byte{byte(a >> 24), byte(a >> 16), byte(a >> 8), byte(a)}, 0
	}
	if v, ok := s.opts[[2]int{level, name}]; ok {
		if len(v) >= 4 {
			return v[:4], 0
		}
		return i32bytes(int(v[0])), 0
	}
	return i32bytes(int(keptDefault[[2]int{level, name}])), 0 // Linux's, before it is set
}
