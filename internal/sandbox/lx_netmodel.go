//go:build darwin || linux || windows

package sandbox

import (
	"net"
	"strconv"
)

// Per-network-namespace interface/address/route model: the control plane of
// laboon's network layer. A non-host netns (one a guest entered with unshare or
// clone CLONE_NEWNET) owns its own network configuration -- the guest brings
// interfaces up, assigns addresses and adds routes with routing netlink, and
// getifaddrs / `ip addr` / `ip route` / RTM_GET* must then report exactly what
// it set. laboon models that state here rather than acknowledging the mutations
// and reporting the host; the old ack-and-fake left a container unable to see
// the very interface and address it had just configured.
//
// The host netns (id 0) still reports the real host interfaces; only an
// unshared netns is modelled. The data plane (vnet) moves the packets and shares
// the netns id as its key.

// netAddr is one address on an interface.
type netAddr struct {
	family int // lxAFInet / lxAFInet6
	ip     []byte
	prefix uint8
}

// netLink is one interface in a netns.
type netLink struct {
	index  int32
	name   string
	kind   string // "" (device), "veth", "bridge", "dummy", "tun", "tap"
	flags  uint32 // IFF_* (bit 0 = IFF_UP)
	mtu    uint32
	hw     [6]byte
	master int32 // IFLA_MASTER: the bridge this link is enslaved to (0 = none)
	peer   int32 // IFLA_LINK: the peer interface index (a veth's other end)
	loop   bool  // loopback
	addrs  []netAddr
}

// netRoute is one route. A nil dst is the default route.
type netRoute struct {
	family   int
	dstLen   uint8
	dst      []byte
	gateway  []byte
	oif      int32
	table    uint32
	priority uint32
	scope    uint8
	proto    uint8
	rtype    uint8
}

// netModel is one netns's interfaces and routes.
type netModel struct {
	links   []*netLink
	routes  []netRoute
	nextIdx int32
}

// seedNetns returns a fresh netns's model: a single loopback interface, DOWN and
// address-less, exactly as the kernel presents a newly unshared netns.
func seedNetns() *netModel {
	return &netModel{
		links:   []*netLink{{index: 1, name: "lo", loop: true, mtu: 65536}},
		nextIdx: 2,
	}
}

// virtBase is the first index given to a link created in the host netns (id 0).
// Host interfaces keep their real, small indices and are reported live; a link a
// guest creates there (a runtime's bridge) is an overlay on top, indexed high so
// it can never collide with a real one.
const virtBase = 1 << 20

// linkModel returns netns id's model, creating it on first use. A non-host netns
// is seeded with lo (down), as the kernel presents a fresh namespace; the host
// netns (id 0) starts empty -- its real interfaces are reported live and only
// the guest's own additions live in the model. Caller holds n.mu.
func (n *dnet) linkModel(id int32) *netModel {
	if n.nets == nil {
		n.nets = map[int32]*netModel{}
	}
	m := n.nets[id]
	if m == nil {
		if id == 0 {
			m = &netModel{nextIdx: virtBase}
		} else {
			m = seedNetns()
		}
		n.nets[id] = m
	}
	return m
}

// byIndexOrName finds a link by index (preferred) or name.
func (m *netModel) byIndexOrName(idx int32, name string) *netLink {
	if idx != 0 {
		return m.byIndex(idx)
	}
	if name != "" {
		return m.byName(name)
	}
	return nil
}

// owns reports whether a mutating message targets a link the model holds (used
// only for the host netns, where a mutation of a real host interface is instead
// acknowledged without touching the host). A typed create (a bridge or veth, by
// IFLA_INFO_KIND) is taken as the model's; everything else is the model's only
// if the target link already exists in it.
func (m *netModel) owns(typ uint16, body []byte) bool {
	switch typ {
	case rtmNewlink, rtmSetlink:
		if len(body) < 16 {
			return false
		}
		at := parseNlAttrs(body[16:])
		if m.byIndexOrName(int32(le32(body[4:])), cstr(at[iflaIfname])) != nil {
			return true
		}
		if typ == rtmNewlink {
			if li := at[iflaLinkinfo]; li != nil && cstr(parseNlAttrs(li)[iflaInfoKind]) != "" {
				return true
			}
		}
		return false
	case rtmDellink:
		return len(body) >= 16 && m.byIndex(int32(le32(body[4:]))) != nil
	case rtmNewaddr, rtmDeladdr:
		return len(body) >= 8 && m.byIndex(int32(le32(body[4:]))) != nil
	}
	return false // routes in the host netns are not modelled
}

func (m *netModel) byIndex(i int32) *netLink {
	for _, l := range m.links {
		if l.index == i {
			return l
		}
	}
	return nil
}

func (m *netModel) byName(name string) *netLink {
	for _, l := range m.links {
		if l.name == name {
			return l
		}
	}
	return nil
}

// addAddr adds an address to the link unless it already has it.
func (l *netLink) addAddr(family int, ip []byte, prefix uint8) {
	for _, a := range l.addrs {
		if a.family == family && string(a.ip) == string(ip) {
			return
		}
	}
	l.addrs = append(l.addrs, netAddr{family: family, ip: append([]byte(nil), ip...), prefix: prefix})
}

// parseNlAttrs walks a netlink attribute stream (after the fixed header) into a
// type->value map. A truncated attribute ends the walk.
func parseNlAttrs(b []byte) map[uint16][]byte {
	out := map[uint16][]byte{}
	for off := 0; off+4 <= len(b); {
		l := int(rd16(b[off:]))
		typ := rd16(b[off+2:]) & 0x3fff // strip NESTED / NET_BYTEORDER flags
		if l < 4 || off+l > len(b) {
			break
		}
		out[typ] = b[off+4 : off+l]
		off += (l + 3) &^ 3
	}
	return out
}

// RTNL attribute and constant values used below.
const (
	nlmFDump = 0x300 // NLM_F_ROOT | NLM_F_MATCH: a dump rather than a single get

	iflaAddress  = 1
	iflaIfname   = 3
	iflaMtu      = 4
	iflaLink     = 5
	iflaMaster   = 10
	iflaNetNsFd  = 28
	iflaLinkinfo = 18
	iflaInfoKind = 1
	iflaInfoData = 2
	vethInfoPeer = 1

	ifaAddress = 1
	ifaLocal   = 2
	ifaLabel   = 3

	rtaDst      = 1
	rtaOif      = 4
	rtaGateway  = 5
	rtaPriority = 6
	rtaTable    = 15

	rtTableMain   = 254
	rtScopeHost   = 254
	rtScopeLink   = 253
	rtScopeUniv   = 0
	rtnUnicast    = 1
	rtProtoStatic = 4
)

// nlModeled answers one routing-netlink message from the model of netns (the
// namespace of the socket the request arrived on). The host netns (id 0) reports
// its real interfaces live and layers the model's own additions on top (a
// runtime's bridge/veth), and a mutation there that does not target a modelled
// link is acknowledged without touching the host; a non-host netns is answered
// entirely from its model. rq is the request message (for the ACK/error
// reply). It takes n.mu for the whole message so concurrent guest
// threads see a consistent model.
func (g *dguest) nlModeled(typ, flags uint16, seq, pid uint32, rq, body []byte, netns int32) []nlOut {
	host := netns == 0 // the host netns reports real interfaces live, plus the model's additions
	n := g.netState()
	n.mu.Lock()
	defer n.mu.Unlock()
	m := n.linkModel(netns)

	family := 0
	if len(body) > 0 {
		family = int(body[0])
	}
	var out []nlOut
	// A dump is never acked, NLM_F_ACK or not: netlink_dump_start returns
	// -EINTR to say so.
	dump := func(msgs [][]byte) []nlOut {
		msgs = append(msgs, nlMsg(nlmsgDone, nlmFMulti, seq, pid, make([]byte, 4)))
		return []nlOut{{dump: msgs, req: append([]byte(nil), rq...)}}
	}
	switch typ {
	case rtmGetlink:
		if flags&nlmFDump != 0 {
			var links [][]byte
			if host {
				links = g.nlLinks(seq, pid) // real host interfaces, live
			}
			out = dump(append(links, g.nlModelLinks(m, seq, pid)...))
		} else {
			// A non-dump GETLINK is a single lookup by index or IFLA_IFNAME: one
			// RTM_NEWLINK reply, or ENODEV. The kernel does not terminate it with
			// NLMSG_DONE.
			idx := int32(0)
			if len(body) >= 8 {
				idx = int32(le32(body[4:]))
			}
			name := ""
			if len(body) > 16 {
				name = cstr(parseNlAttrs(body[16:])[iflaIfname])
			}
			var one []byte
			if l := m.byIndexOrName(idx, name); l != nil {
				one = g.nlLinkMsg(l, 0, seq, pid)
			} else if host {
				one = g.nlHostLink(idx, name, seq, pid)
			}
			if one == nil {
				out = append(out, nlOut{dgram: nlErr(seq, pid, lxENODEV, rq)})
			} else {
				out = append(out, nlOut{dgram: one})
			}
		}
	case rtmGetaddr:
		var addrs [][]byte
		if host {
			addrs = g.nlAddrs(seq, pid, family) // real host addresses, live
		}
		out = dump(append(addrs, g.nlModelAddrs(m, seq, pid, family)...))
	case rtmGetroute:
		out = dump(g.nlModelRoutes(m, seq, pid, family))
	case rtmNewlink, rtmSetlink, rtmDellink, rtmNewaddr, rtmDeladdr, rtmNewroute, rtmDelroute:
		if peerBody, ok := vethPeer(body); typ == rtmNewlink && ok {
			// A veth pair: the primary end in this socket's namespace and the peer
			// in the namespace its IFLA_NET_NS_FD names (how a runtime wires a
			// container). Created across both models here, since nlConfigure sees
			// only one namespace's model.
			target := netns
			if fd := vethPeerNetns(peerBody); fd >= 0 {
				target = g.resolveNetnsFd(fd, netns)
			}
			n.createVeth(m, body, peerBody, target)
			if flags&nlmFAck != 0 {
				out = append(out, nlOut{dgram: nlErr(seq, pid, 0, rq)})
			}
		} else if host && !m.owns(typ, body) {
			// A mutation of a real host interface (or a host route) is acknowledged
			// without touching the host, as it was before any modelling.
			if flags&nlmFAck != 0 {
				out = append(out, nlOut{dgram: nlErr(seq, pid, 0, rq)})
			}
		} else {
			e := g.nlConfigure(m, typ, body)
			if e != 0 { // the kernel always reports an error, ack or not
				out = append(out, nlOut{dgram: nlErr(seq, pid, e, rq)})
			} else {
				if netns != 0 {
					n.syncIPNetns(netns, m) // feed vnet delivery from the model
				}
				if flags&nlmFAck != 0 {
					out = append(out, nlOut{dgram: nlErr(seq, pid, 0, rq)})
				}
			}
		}
	default:
		out = append(out, nlOut{dgram: nlErr(seq, pid, lxEOPNOTSUPP, rq)})
	}
	return out
}

// vethPeer returns the nested peer LinkMessage of a veth RTM_NEWLINK (the bytes
// after IFLA_LINKINFO/IFLA_INFO_DATA/VETH_INFO_PEER), or ok=false when the
// message is not a veth create carrying a peer. The kernel creates a veth pair
// from one message: the primary end in the sending socket's namespace and the
// peer described here, which may name its own target namespace.
func vethPeer(body []byte) ([]byte, bool) {
	if len(body) < 16 {
		return nil, false
	}
	li := parseNlAttrs(body[16:])[iflaLinkinfo]
	if li == nil {
		return nil, false
	}
	sub := parseNlAttrs(li)
	if cstr(sub[iflaInfoKind]) != "veth" || sub[iflaInfoData] == nil {
		return nil, false
	}
	peer := parseNlAttrs(sub[iflaInfoData])[vethInfoPeer]
	if peer == nil {
		return nil, false
	}
	return peer, true
}

// vethPeerNetns returns the IFLA_NET_NS_FD the peer LinkMessage carries (the
// descriptor of the namespace the peer end should be created in), or -1.
func vethPeerNetns(peerBody []byte) int {
	if len(peerBody) < 16 {
		return -1
	}
	if fd := parseNlAttrs(peerBody[16:])[iflaNetNsFd]; len(fd) >= 4 {
		return int(int32(le32(fd)))
	}
	return -1
}

// newVethEnd builds one end of a veth pair in model m from a LinkMessage's
// attributes. An empty name (the kernel auto-names the primary end) becomes
// vethN. Caller holds n.mu.
func newVethEnd(m *netModel, at map[uint16][]byte) *netLink {
	l := &netLink{index: m.nextIdx, kind: "veth", mtu: 1500, name: cstr(at[iflaIfname])}
	m.nextIdx++
	if l.name == "" {
		l.name = "veth" + strconv.Itoa(int(l.index))
	}
	if mtu := at[iflaMtu]; len(mtu) >= 4 {
		l.mtu = le32(mtu)
	}
	if mac := at[iflaAddress]; len(mac) >= 6 {
		copy(l.hw[:], mac)
	}
	if mst := at[iflaMaster]; len(mst) >= 4 {
		l.master = int32(le32(mst))
	}
	return l
}

// createVeth creates a veth pair: the primary end in m (the sending socket's
// namespace) and the peer end in the target namespace's model, each pointing at
// the other via IFLA_LINK. Caller holds n.mu.
func (n *dnet) createVeth(m *netModel, body, peerBody []byte, target int32) {
	host := newVethEnd(m, parseNlAttrs(body[16:]))
	pm := n.linkModel(target)
	peer := newVethEnd(pm, parseNlAttrs(peerBody[16:]))
	host.peer, peer.peer = peer.index, host.index
	m.links = append(m.links, host)
	pm.links = append(pm.links, peer)
}

// resolveNetnsFd maps a guest fd referring to a network-namespace file to its
// laboon namespace id, via the bind mount that pins it (how a runtime hands a
// container's netns to netavark). It returns fallback when the fd is not a known
// pinned namespace.
func (g *dguest) resolveNetnsFd(fd int, fallback int32) int32 {
	if d, ok := g.ft.get(fd); ok {
		if ns, ok := g.dfs.nsBindNetns(d.name()); ok {
			return ns
		}
	}
	return fallback
}

// nlConfigure applies a mutating RTNL message to the model m. body is the
// message after the netlink header. Caller holds n.mu.
func (g *dguest) nlConfigure(m *netModel, typ uint16, body []byte) lxErrno {
	switch typ {
	case rtmNewlink, rtmSetlink:
		if len(body) < 16 {
			return lxEINVAL
		}
		// struct ifinfomsg: family, _, type(2), index(4), flags(4), change(4).
		index := int32(le32(body[4:]))
		flags := le32(body[8:])
		change := le32(body[12:])
		at := parseNlAttrs(body[16:])
		name := cstr(at[iflaIfname])
		var l *netLink
		switch {
		case index != 0:
			l = m.byIndex(index)
		case name != "":
			l = m.byName(name)
		}
		if l == nil {
			if typ == rtmSetlink {
				return lxENODEV
			}
			l = &netLink{index: m.nextIdx, name: name, mtu: 1500}
			m.nextIdx++
			if li := at[iflaLinkinfo]; li != nil {
				l.kind = cstr(parseNlAttrs(li)[iflaInfoKind])
			}
			m.links = append(m.links, l)
		}
		if mtu := at[iflaMtu]; len(mtu) >= 4 {
			l.mtu = le32(mtu)
		}
		if mac := at[iflaAddress]; len(mac) >= 6 {
			copy(l.hw[:], mac)
		}
		if mst := at[iflaMaster]; len(mst) >= 4 {
			l.master = int32(le32(mst)) // bridge enslave
		}
		if lk := at[iflaLink]; len(lk) >= 4 {
			l.peer = int32(le32(lk))
		}
		if index != 0 && name != "" {
			l.name = name // SETLINK by index carrying IFLA_IFNAME renames
		}
		if change != 0 { // apply only the flag bits the caller is changing
			was := l.flags&lxIffUp != 0
			l.flags = l.flags&^change | flags&change
			if l.loop && !was && l.flags&lxIffUp != 0 {
				// The kernel assigns the loopback its host addresses when it is
				// brought up (127.0.0.1/8 and ::1/128); a container runtime relies
				// on that rather than adding them itself. (The kernel also drops
				// ::1 when lo goes back down; not modelled -- lo is not toggled.)
				l.addAddr(lxAFInet, []byte{127, 0, 0, 1}, 8)
				l.addAddr(lxAFInet6, net.IPv6loopback, 128)
			}
		}
		return 0
	case rtmDellink:
		if len(body) < 16 {
			return lxEINVAL
		}
		index := int32(le32(body[4:]))
		for i, l := range m.links {
			if l.index == index {
				m.links = append(m.links[:i], m.links[i+1:]...)
				return 0
			}
		}
		return lxENODEV
	case rtmNewaddr, rtmDeladdr:
		if len(body) < 8 {
			return lxEINVAL
		}
		// struct ifaddrmsg: family, prefixlen, flags, scope, index(4).
		fam := int(body[0])
		prefix := body[1]
		index := int32(le32(body[4:]))
		l := m.byIndex(index)
		if l == nil {
			return lxENODEV
		}
		at := parseNlAttrs(body[8:])
		raw := at[ifaLocal]
		if raw == nil {
			raw = at[ifaAddress]
		}
		ip := append([]byte(nil), raw...)
		if typ == rtmDeladdr {
			for i, a := range l.addrs {
				if a.family == fam && string(a.ip) == string(ip) {
					l.addrs = append(l.addrs[:i], l.addrs[i+1:]...)
					break
				}
			}
			return 0
		}
		if len(ip) == 0 {
			return lxEINVAL
		}
		l.addAddr(fam, ip, prefix) // idempotent: a re-added address is not duplicated
		return 0
	case rtmNewroute, rtmDelroute:
		if len(body) < 12 {
			return lxEINVAL
		}
		// struct rtmsg: family, dst_len, src_len, tos, table, protocol, scope, type, flags(4).
		r := netRoute{
			family: int(body[0]),
			dstLen: body[1],
			table:  uint32(body[4]),
			proto:  body[5],
			scope:  body[6],
			rtype:  body[7],
		}
		at := parseNlAttrs(body[12:])
		if d := at[rtaDst]; d != nil {
			r.dst = append([]byte(nil), d...)
		}
		if gw := at[rtaGateway]; gw != nil {
			r.gateway = append([]byte(nil), gw...)
		}
		if o := at[rtaOif]; len(o) >= 4 {
			r.oif = int32(le32(o))
		}
		if p := at[rtaPriority]; len(p) >= 4 {
			r.priority = le32(p)
		}
		if t := at[rtaTable]; len(t) >= 4 {
			r.table = le32(t)
		}
		if typ == rtmDelroute {
			for i, e := range m.routes {
				if e.family == r.family && e.dstLen == r.dstLen && e.table == r.table && string(e.dst) == string(r.dst) {
					m.routes = append(m.routes[:i], m.routes[i+1:]...)
					break
				}
			}
			return 0
		}
		m.routes = append(m.routes, r)
		return 0
	}
	return lxEOPNOTSUPP
}

// nlLinkMsg serializes one interface as an RTM_NEWLINK message with the given
// netlink flags (nlmFMulti in a dump, 0 for a single get).
func (g *dguest) nlLinkMsg(l *netLink, flags uint16, seq, pid uint32) []byte {
	b := make([]byte, 16) // struct ifinfomsg
	arphrd := uint16(1)   // ARPHRD_ETHER
	if l.loop {
		arphrd = 772 // ARPHRD_LOOPBACK
	}
	b[2], b[3] = byte(arphrd), byte(arphrd>>8)
	put32(b[4:], uint32(l.index))
	put32(b[8:], l.flags)
	b = append(b, rtattr(iflaIfname, append([]byte(l.name), 0))...)
	b = append(b, rtattr(iflaAddress, l.hw[:])...)
	var mtu [4]byte
	put32(mtu[:], l.mtu)
	b = append(b, rtattr(iflaMtu, mtu[:])...)
	if l.master != 0 {
		var mi [4]byte
		put32(mi[:], uint32(l.master))
		b = append(b, rtattr(iflaMaster, mi[:])...)
	}
	if l.peer != 0 {
		var pi [4]byte
		put32(pi[:], uint32(l.peer))
		b = append(b, rtattr(iflaLink, pi[:])...)
	}
	if l.kind != "" { // IFLA_LINKINFO { IFLA_INFO_KIND } -- netavark checks the kind
		b = append(b, rtattr(iflaLinkinfo, rtattr(iflaInfoKind, append([]byte(l.kind), 0)))...)
	}
	return nlMsg(rtmNewlink, flags, seq, pid, b)
}

// nlModelLinks serializes the model's interfaces as RTM_NEWLINK dump messages.
func (g *dguest) nlModelLinks(m *netModel, seq, pid uint32) [][]byte {
	var msgs [][]byte
	for _, l := range m.links {
		msgs = append(msgs, g.nlLinkMsg(l, nlmFMulti, seq, pid))
	}
	return msgs
}

// nlModelAddrs serializes the model's addresses as RTM_NEWADDR dump messages.
func (g *dguest) nlModelAddrs(m *netModel, seq, pid uint32, family int) [][]byte {
	var msgs [][]byte
	for _, l := range m.links {
		for _, a := range l.addrs {
			if family != 0 && family != a.family {
				continue
			}
			scope := byte(rtScopeUniv)
			if ip := net.IP(a.ip); ip.IsLoopback() {
				scope = rtScopeHost
			} else if ip.IsLinkLocalUnicast() {
				scope = rtScopeLink
			}
			b := []byte{byte(a.family), a.prefix, 0x80, scope, 0, 0, 0, 0} // ifaddrmsg, IFA_F_PERMANENT
			put32(b[4:], uint32(l.index))
			b = append(b, rtattr(ifaAddress, a.ip)...)
			if a.family == lxAFInet {
				b = append(b, rtattr(ifaLocal, a.ip)...)
				b = append(b, rtattr(ifaLabel, append([]byte(l.name), 0))...)
			}
			msgs = append(msgs, nlMsg(rtmNewaddr, nlmFMulti, seq, pid, b))
		}
	}
	return msgs
}

// nlModelRoutes serializes the model's routes as RTM_NEWROUTE dump messages.
func (g *dguest) nlModelRoutes(m *netModel, seq, pid uint32, family int) [][]byte {
	var msgs [][]byte
	for _, r := range m.routes {
		if family != 0 && family != r.family {
			continue
		}
		table := byte(r.table)
		if r.table > 255 {
			table = rtTableMain
		}
		proto := r.proto
		if proto == 0 {
			proto = rtProtoStatic
		}
		rtype := r.rtype
		if rtype == 0 {
			rtype = rtnUnicast
		}
		b := []byte{byte(r.family), r.dstLen, 0, 0, table, proto, r.scope, rtype, 0, 0, 0, 0} // struct rtmsg
		if r.dst != nil {
			b = append(b, rtattr(rtaDst, r.dst)...)
		}
		if r.gateway != nil {
			b = append(b, rtattr(rtaGateway, r.gateway)...)
		}
		if r.oif != 0 {
			var o [4]byte
			put32(o[:], uint32(r.oif))
			b = append(b, rtattr(rtaOif, o[:])...)
		}
		if r.priority != 0 {
			var p [4]byte
			put32(p[:], r.priority)
			b = append(b, rtattr(rtaPriority, p[:])...)
		}
		if r.table > 255 {
			var t [4]byte
			put32(t[:], r.table)
			b = append(b, rtattr(rtaTable, t[:])...)
		}
		msgs = append(msgs, nlMsg(rtmNewroute, nlmFMulti, seq, pid, b))
	}
	return msgs
}
