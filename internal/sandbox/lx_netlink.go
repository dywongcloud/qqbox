//go:build darwin || linux || windows

package sandbox

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

// NETLINK_ROUTE, the part of it libc's getifaddrs and if_nameindex use: link
// and address dumps (RTM_GETLINK, RTM_GETADDR), answered from the host's
// interfaces (only loopback without Config.Net, as in an empty network
// namespace). Each host queues a request's replies on the socket before send
// returns (nlReplies builds them), so recv, MSG_DONTWAIT and poll see them as
// on Linux.

const (
	lxAFNetlink  = 16
	lxSolNetlink = 270
	lxSockRaw    = 3

	nlmsgError  = 2
	nlmsgDone   = 3
	rtmNewlink  = 16
	rtmDellink  = 17
	rtmGetlink  = 18
	rtmSetlink  = 19
	rtmNewaddr  = 20
	rtmDeladdr  = 21
	rtmGetaddr  = 22
	rtmNewroute = 24
	rtmDelroute = 25
	rtmGetroute = 26
	nlmFMulti   = 2
	nlmFAck     = 4
	nlmFCapped  = 0x100
	nlmsgHdrLen = 16

	lxIffUp        = 0x1
	lxIffBroadcast = 0x2
	lxIffLoopback  = 0x8
	lxIffP2P       = 0x10
	lxIffRunning   = 0x40
	lxIffMulticast = 0x1000

	// skbOverhead is SKB_DATA_ALIGN(sizeof(struct skb_shared_info)), what a
	// netlink skb's allocation loses to the kernel's own bookkeeping.
	skbOverhead = 320
	// nlRecvMax is the most a socket's largest receive buffer counts for
	// (netlink_recvmsg: SKB_WITH_OVERHEAD(32768)).
	nlRecvMax = 32768 - skbOverhead
)

// sockaddrNl is a struct sockaddr_nl: the kernel's (pid 0) or this socket's.
func sockaddrNl(pid int) []byte {
	b := make([]byte, 12)
	b[0] = lxAFNetlink
	put32(b[4:], uint32(pid))
	return b
}

// nlOut is one reply to a netlink request: a datagram of its own (an ack, an
// error, the answer to a lookup), or a dump's messages, NLMSG_DONE last, which
// the socket hands out as the guest reads (nlDump). req is a dump request's
// header, for the error a second dump meets.
type nlOut struct {
	dgram []byte
	dump  [][]byte
	req   []byte
}

// nlDump is a socket's running dump. Linux fills one datagram as it starts
// the dump and one more each time the guest receives, as large as the
// largest buffer the socket has been read with but no smaller than
// NLMSG_GOODSIZE, and lets one dump run at a time.
type nlDump struct {
	mu      sync.Mutex
	msgs    [][]byte // what the running dump has yet to hand out; nil when none runs
	maxRecv int      // max_recvmsg_len
	good    int      // NLMSG_GOODSIZE: SKB_WITH_OVERHEAD(min(PAGE_SIZE, 8192))
}

func newNlDump(page int) *nlDump {
	return &nlDump{good: min(page, 8192) - skbOverhead}
}

// queue turns a request's replies into the datagrams the socket receives
// now: each datagram of its own, and the first of a dump. A dump asked for
// while one runs fails with EBUSY.
func (d *nlDump) queue(outs []nlOut, pid uint32) [][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	var dgs [][]byte
	for _, o := range outs {
		switch {
		case o.dump == nil:
			dgs = append(dgs, o.dgram)
		case d.msgs != nil:
			dgs = append(dgs, nlErr(le32(o.req[8:]), pid, lxEBUSY, o.req))
		default:
			d.msgs = o.dump
			dgs = append(dgs, d.next())
		}
	}
	return dgs
}

// received notes a receive with an n-byte buffer, and returns the running
// dump's next datagram, or nil.
func (d *nlDump) received(n int) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maxRecv = min(max(d.maxRecv, n), nlRecvMax)
	if d.msgs == nil {
		return nil
	}
	return d.next()
}

// next packs the running dump's next datagram: as many of its messages as
// fit, and always at least one. NLMSG_DONE comes in a datagram of its own: a
// dump callback that added anything reports more may follow, so the kernel
// learns the dump is over only on its next call. d.mu held.
func (d *nlDump) next() []byte {
	limit := max(d.good, d.maxRecv)
	var b []byte
	for len(d.msgs) > 0 && (len(b) == 0 || len(b)+len(d.msgs[0]) <= limit && !isDone(d.msgs[0])) {
		b = append(b, d.msgs[0]...)
		d.msgs = d.msgs[1:]
	}
	if len(d.msgs) == 0 {
		d.msgs = nil
	}
	return b
}

// isDone reports whether netlink message m is NLMSG_DONE, which ends a dump.
func isDone(m []byte) bool {
	return len(m) >= nlmsgHdrLen && uint16(m[4])|uint16(m[5])<<8 == nlmsgDone
}

// nlReplies answers the netlink requests in req, in order.
func (g *dguest) nlReplies(req []byte, netns int32) []nlOut {
	pid := uint32(g.tgid) // replies are addressed to the socket's port id
	var out []nlOut
	for off := 0; off+nlmsgHdrLen <= len(req); {
		l := int(le32(req[off:]))
		if l < nlmsgHdrLen || off+l > len(req) {
			break
		}
		typ := uint16(req[off+4]) | uint16(req[off+5])<<8
		flags := uint16(req[off+6]) | uint16(req[off+7])<<8
		seq := le32(req[off+8:])
		rq := req[off : off+l] // the request message, which an error echoes
		body := req[off+nlmsgHdrLen : off+l]
		// Every namespace is answered from its model. netns is the namespace of
		// the socket the request came on (captured at socket time), not the
		// caller's current one -- a runtime makes a socket inside the container
		// netns and then drives it from the host. The host namespace (id 0)
		// reports its real interfaces live and layers its own additions on top.
		out = append(out, g.nlModeled(typ, flags, seq, pid, rq, body, netns)...)
		off += (l + 3) &^ 3
	}
	return out
}

func nlMsg(typ, flags uint16, seq, pid uint32, body []byte) []byte {
	b := make([]byte, nlmsgHdrLen, nlmsgHdrLen+len(body)+3)
	put32(b, uint32(nlmsgHdrLen+len(body)))
	b[4], b[5] = byte(typ), byte(typ>>8)
	b[6], b[7] = byte(flags), byte(flags>>8)
	put32(b[8:], seq)
	put32(b[12:], pid)
	b = append(b, body...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// nlErr is the NLMSG_ERROR answering request message rq, as netlink_ack
// builds it: an ack (e == 0) echoes rq's header and is NLM_F_CAPPED, an error
// carries -e and echoes all of rq.
func nlErr(seq, pid uint32, e lxErrno, rq []byte) []byte {
	body := make([]byte, 4, 4+len(rq))
	flags := uint16(0)
	if e != 0 {
		put32(body, uint32(-int64(e)))
	} else {
		rq, flags = rq[:min(len(rq), nlmsgHdrLen)], nlmFCapped
	}
	return nlMsg(nlmsgError, flags, seq, pid, append(body, rq...))
}

func rtattr(typ uint16, data []byte) []byte {
	b := make([]byte, 4, 4+len(data)+3)
	b[0], b[1] = byte(4+len(data)), byte((4+len(data))>>8)
	b[2], b[3] = byte(typ), byte(typ>>8)
	b = append(b, data...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// guestIfaces returns the interfaces the guest sees: the host's with
// networking enabled, else loopback alone. The host's lo0 is named lo.
func (g *dguest) guestIfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	eth := 0
	for _, ifc := range all {
		lo := ifc.Flags&net.FlagLoopback != 0
		if !lo && !g.hostNet() {
			continue
		}
		switch {
		case lo:
			ifc.Name = "lo"
		case !linuxIfName(ifc.Name):
			// A Windows name ("Ethernet 2") is too long or has spaces.
			ifc.Name = fmt.Sprintf("eth%d", eth)
			eth++
		}
		out = append(out, ifc)
	}
	return out
}

// linuxIfName reports whether a Linux interface could be called name: at
// most IFNAMSIZ-1 bytes, without spaces, slashes or colons.
func linuxIfName(name string) bool {
	return name != "" && len(name) < 16 && !strings.ContainsAny(name, " /:")
}

func ifFlags(f net.Flags) uint32 {
	var lx uint32
	if f&net.FlagUp != 0 {
		lx |= lxIffUp
	}
	if f&net.FlagRunning != 0 {
		lx |= lxIffRunning
	}
	if f&net.FlagBroadcast != 0 {
		lx |= lxIffBroadcast
	}
	if f&net.FlagLoopback != 0 {
		lx |= lxIffLoopback
	}
	if f&net.FlagPointToPoint != 0 {
		lx |= lxIffP2P
	}
	if f&net.FlagMulticast != 0 {
		lx |= lxIffMulticast
	}
	return lx
}

// nlHostLinkMsg serializes one real host interface as an RTM_NEWLINK message
// with the given netlink flags (nlmFMulti in a dump, 0 for a single get).
func (g *dguest) nlHostLinkMsg(ifc net.Interface, flags uint16, seq, pid uint32) []byte {
	body := make([]byte, 16) // struct ifinfomsg
	arphrd := uint16(1)      // ARPHRD_ETHER
	if ifc.Flags&net.FlagLoopback != 0 {
		arphrd = 772 // ARPHRD_LOOPBACK
	}
	body[2], body[3] = byte(arphrd), byte(arphrd>>8)
	put32(body[4:], uint32(ifc.Index))
	put32(body[8:], ifFlags(ifc.Flags))
	body = append(body, rtattr(3, append([]byte(ifc.Name), 0))...) // IFLA_IFNAME
	hw := ifc.HardwareAddr
	if len(hw) == 0 {
		hw = make(net.HardwareAddr, 6)
	}
	body = append(body, rtattr(1, hw)...) // IFLA_ADDRESS
	if ifc.Flags&net.FlagBroadcast != 0 {
		body = append(body, rtattr(2, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})...) // IFLA_BROADCAST
	}
	var mtu [4]byte
	put32(mtu[:], uint32(ifc.MTU))
	body = append(body, rtattr(4, mtu[:])...) // IFLA_MTU
	return nlMsg(rtmNewlink, flags, seq, pid, body)
}

func (g *dguest) nlLinks(seq, pid uint32) [][]byte {
	var msgs [][]byte
	for _, ifc := range g.guestIfaces() {
		msgs = append(msgs, g.nlHostLinkMsg(ifc, nlmFMulti, seq, pid))
	}
	return msgs
}

// nlHostLink builds the single RTM_NEWLINK for the real host interface matched by
// index or name, or nil if none matches.
func (g *dguest) nlHostLink(idx int32, name string, seq, pid uint32) []byte {
	for _, ifc := range g.guestIfaces() {
		if (idx != 0 && int32(ifc.Index) == idx) || (name != "" && ifc.Name == name) {
			return g.nlHostLinkMsg(ifc, 0, seq, pid)
		}
	}
	return nil
}

func (g *dguest) nlAddrs(seq, pid uint32, family int) [][]byte {
	var msgs [][]byte
	for _, ifc := range g.guestIfaces() {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ones, _ := ipn.Mask.Size()
			ip := ipn.IP.To4()
			fam := lxAFInet
			if ip == nil {
				ip, fam = ipn.IP.To16(), lxAFInet6
			}
			if family != 0 && family != fam {
				continue
			}
			scope := byte(0) // RT_SCOPE_UNIVERSE
			switch {
			case ipn.IP.IsLoopback():
				scope = 254 // RT_SCOPE_HOST
			case ipn.IP.IsLinkLocalUnicast():
				scope = 253 // RT_SCOPE_LINK
			}
			body := []byte{byte(fam), byte(ones), 0x80, scope, 0, 0, 0, 0} // struct ifaddrmsg, IFA_F_PERMANENT
			put32(body[4:], uint32(ifc.Index))
			body = append(body, rtattr(1, ip)...) // IFA_ADDRESS
			if fam == lxAFInet {
				body = append(body, rtattr(2, ip)...) // IFA_LOCAL
				if ifc.Flags&net.FlagBroadcast != 0 && ones < 31 {
					bc := make(net.IP, 4)
					for i := range bc {
						bc[i] = ip[i] | ^ipn.Mask[len(ipn.Mask)-4+i]
					}
					body = append(body, rtattr(4, bc)...) // IFA_BROADCAST
				}
				body = append(body, rtattr(3, append([]byte(ifc.Name), 0))...) // IFA_LABEL
			}
			msgs = append(msgs, nlMsg(rtmNewaddr, nlmFMulti, seq, pid, body))
		}
	}
	return msgs
}

// Linux interface ioctls on a socket, answered from the guest's interfaces.
const (
	lxSiocgifname    = 0x8910
	lxSiocgifconf    = 0x8912
	lxSiocgifflags   = 0x8913
	lxSiocsifflags   = 0x8914
	lxSiocgifaddr    = 0x8915
	lxSiocgifbrdaddr = 0x8919
	lxSiocgifnetmask = 0x891b
	lxSiocgifmtu     = 0x8921
	lxSiocgifhwaddr  = 0x8927
	lxSiocgifindex   = 0x8933
	lxSiocgiftxqlen  = 0x8942

	ifreqSize = 40 // struct ifreq: char[16] name + 24-byte union
)

// ifIoctl serves the SIOCGIF* requests (struct ifreq at arg, or struct
// ifconf for SIOCGIFCONF). ok is false for any other request.
func (g *dguest) ifIoctl(req, arg uint64) (int64, bool) {
	switch req {
	case lxSiocgifconf:
		return g.ifconf(arg), true
	case lxSiocsifflags:
		// Set interface flags. laboon's loopback is modeled as always up, so a
		// runtime's "bring lo up" is accepted as a no-op; other interfaces and
		// down requests are not configurable.
		b, err := g.readMem(arg, ifreqSize)
		if err != nil {
			return sErr(lxEFAULT), true
		}
		if cstr(b[:16]) != "lo" {
			return sErr(lxENODEV), true
		}
		return 0, true
	case lxSiocgifname, lxSiocgifflags, lxSiocgifaddr, lxSiocgifbrdaddr, lxSiocgifnetmask,
		lxSiocgifmtu, lxSiocgifhwaddr, lxSiocgifindex, lxSiocgiftxqlen:
	default:
		return 0, false
	}
	b, err := g.readMem(arg, ifreqSize)
	if err != nil {
		return sErr(lxEFAULT), true
	}
	ifaces := g.guestIfaces()
	var ifc *net.Interface
	for i := range ifaces {
		if req == lxSiocgifname && ifaces[i].Index == int(int32(le32(b[16:]))) ||
			req != lxSiocgifname && ifaces[i].Name == cstr(b[:16]) {
			ifc = &ifaces[i]
			break
		}
	}
	if ifc == nil {
		return sErr(lxENODEV), true
	}
	u := b[16:]
	switch req {
	case lxSiocgifname:
		copy(b[:16], make([]byte, 16))
		copy(b[:15], ifc.Name)
	case lxSiocgifindex:
		put32(u, uint32(ifc.Index))
	case lxSiocgifflags:
		f := ifFlags(ifc.Flags)
		u[0], u[1] = byte(f), byte(f>>8)
	case lxSiocgifmtu:
		put32(u, uint32(ifc.MTU))
	case lxSiocgiftxqlen:
		put32(u, 1000)
	case lxSiocgifhwaddr:
		arphrd := uint16(1)
		if ifc.Flags&net.FlagLoopback != 0 {
			arphrd = 772
		}
		copy(u, make([]byte, 16))
		u[0], u[1] = byte(arphrd), byte(arphrd>>8)
		copy(u[2:16], ifc.HardwareAddr)
	default:
		ipn := ifaceV4(ifc)
		if ipn == nil {
			return sErr(lxEADDRNOTAVAIL), true
		}
		ip := ipn.IP.To4()
		switch req {
		case lxSiocgifnetmask:
			ip = net.IP(ipn.Mask[len(ipn.Mask)-4:])
		case lxSiocgifbrdaddr:
			bc := make(net.IP, 4)
			for i := range bc {
				bc[i] = ip[i] | ^ipn.Mask[len(ipn.Mask)-4+i]
			}
			ip = bc
		}
		copy(u, make([]byte, 16))
		u[0] = lxAFInet
		copy(u[4:8], ip)
	}
	if err := g.writeMem(arg, b); err != nil {
		return sErr(lxEFAULT), true
	}
	return 0, true
}

func cstr(b []byte) string {
	if i := indexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func ifaceV4(ifc *net.Interface) *net.IPNet {
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn
		}
	}
	return nil
}

// ifconf fills struct ifconf {int ifc_len; char *ifc_buf} with one ifreq per
// IPv4 address; a NULL buffer asks only for the length needed.
func (g *dguest) ifconf(arg uint64) int64 {
	b, err := g.readMem(arg, 16)
	if err != nil {
		return sErr(lxEFAULT)
	}
	room, buf := int(int32(le32(b))), le64(b[8:])
	var out []byte
	for _, ifc := range g.guestIfaces() {
		ipn := ifaceV4(&ifc)
		if ipn == nil {
			continue
		}
		r := make([]byte, ifreqSize)
		copy(r[:15], ifc.Name)
		r[16] = lxAFInet
		copy(r[20:24], ipn.IP.To4())
		out = append(out, r...)
	}
	if buf != 0 {
		out = out[:min(len(out), max(room, 0)/ifreqSize*ifreqSize)]
		if err := g.writeMem(buf, out); err != nil {
			return sErr(lxEFAULT)
		}
	}
	put32(b, uint32(len(out)))
	if err := g.writeMem(arg, b[:4]); err != nil {
		return sErr(lxEFAULT)
	}
	return 0
}
