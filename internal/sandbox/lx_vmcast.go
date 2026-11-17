//go:build darwin || linux || windows

package sandbox

import "net"

// Multicast as Linux does it, over the modelled interfaces: what every
// backend's in-process loopback shares (vmcast_unix.go, vmcast_windows.go).
// A socket joins a group on one interface: the one it names, by index or
// address, or the one the route to the group leaves by (Linux's
// ip_mc_find_dev). An interface takes a group in once any socket joined it
// there. A datagram to a group leaves by IP_MULTICAST_IF's interface or the
// route's, comes back to the sender's own namespace unless IP_MULTICAST_LOOP
// is off, and with a TTL of at least 1 reaches every namespace on that
// interface's link; out of a loopback interface, it comes back whatever both
// say. In each namespace, it goes to every socket bound to its port on the
// wildcard or the group, when the receiving interface took the group in and
// the socket joined it there or takes every group (IP_MULTICAST_ALL), and,
// connected, only from its peer.
//
// IPv6 groups are joined and left with Linux's answers, and bound to, but
// not delivered: the only interface multicast could leave by without a
// route, lo, carries none for IPv6, and no in-process loopback models the
// IPv6 addresses of the modelled interfaces that would.

// mcMember is one group a socket joined, on the interface link.
type mcMember struct {
	link  int32
	group uint32
}

// mc6Member is one IPv6 group a socket joined, on the interface link.
type mc6Member struct {
	link  int32
	group [16]byte
}

// mcDevKey is a group an interface of a namespace took in.
type mcDevKey struct {
	netns int32
	link  int32
	group uint32
}

// mcMaxMemberships is net.ipv4.igmp_max_memberships's default: how many
// groups one socket may join.
const mcMaxMemberships = 20

func multicast4(ip uint32) bool { return ip>>28 == 0xe }

// vmcLink is an interface of a namespace, as multicast sees it.
type vmcLink struct {
	index int32
	loop  bool
	addrs []uint32 // its IPv4 addresses
	v6    bool     // it holds an IPv6 address and takes multicast (the kernel's ff00::/8 route)
}

// vmcLinks is the interfaces of netns a vnet socket's multicast sees: the
// model's, or for the host namespace its loopback alone, the only interface
// a vnet socket there is on. The caller holds n.mu.
func (n *dnet) vmcLinks(netns int32) []vmcLink {
	if netns == 0 {
		ifs, err := net.Interfaces()
		if err != nil {
			return nil
		}
		for _, ifc := range ifs {
			if ifc.Flags&net.FlagLoopback != 0 {
				return []vmcLink{{index: int32(ifc.Index), loop: true, addrs: []uint32{127<<24 | 1}}}
			}
		}
		return nil
	}
	m := n.nets[netns]
	if m == nil {
		return nil
	}
	out := make([]vmcLink, 0, len(m.links))
	for _, l := range m.links {
		v := vmcLink{index: l.index, loop: l.loop}
		for _, a := range l.addrs {
			switch {
			case a.family == lxAFInet && len(a.ip) == 4:
				v.addrs = append(v.addrs, beIP(a.ip))
			case a.family == lxAFInet6 && !l.loop && l.flags&lxIffUp != 0:
				v.v6 = true
			}
		}
		out = append(out, v)
	}
	return out
}

// vmcFind resolves the interface of a multicast membership or send in netns
// as Linux's ip_mc_find_dev does: by index, else by an IPv4 address an
// interface holds, else by the route to the group. 0 when there is none. The
// caller holds n.mu.
func (n *dnet) vmcFind(netns int32, index int32, addr, group uint32) int32 {
	links := n.vmcLinks(netns)
	switch {
	case index != 0:
		for _, l := range links {
			if l.index == index {
				return index
			}
		}
		return 0
	case addr != 0:
		for _, l := range links {
			for _, a := range l.addrs {
				if a == addr {
					return l.index
				}
			}
		}
		return 0
	}
	return n.vmcRoute(netns, group)
}

// vmcRoute is the interface the route of netns to group leaves by: the
// longest matching prefix, then the lowest metric, of its unicast and
// multicast routes; 0 when none covers the group. The host namespace's
// loopback carries no such route. The caller holds n.mu.
func (n *dnet) vmcRoute(netns int32, group uint32) int32 {
	m := n.nets[netns]
	if netns == 0 || m == nil {
		return 0
	}
	var best *netRoute
	for i := range m.routes {
		r := &m.routes[i]
		if r.family != lxAFInet || (r.rtype != 0 && r.rtype != 1 && r.rtype != 5) { // RTN_UNICAST, RTN_MULTICAST
			continue
		}
		var dst uint32
		if len(r.dst) == 4 {
			dst = beIP(r.dst)
		}
		if mask := prefixMask(r.dstLen); group&mask != dst&mask {
			continue
		}
		if best == nil || r.dstLen > best.dstLen || (r.dstLen == best.dstLen && r.priority < best.priority) {
			best = r
		}
	}
	switch {
	case best == nil:
		return 0
	case best.oif != 0:
		return best.oif
	case len(best.gateway) == 4:
		gw := beIP(best.gateway)
		for _, l := range m.links {
			for _, a := range l.addrs {
				if mask := prefixMask(a.prefix); a.family == lxAFInet && len(a.ip) == 4 && beIP(a.ip)&mask == gw&mask {
					return l.index
				}
			}
		}
	}
	return 0
}

// vmcRoute6 is the interface an IPv6 group's route leaves by: the first up
// interface holding an IPv6 address, where the kernel puts its ff00::/8
// route; 0 when there is none. The caller holds n.mu.
func (n *dnet) vmcRoute6(netns int32) int32 {
	for _, l := range n.vmcLinks(netns) {
		if l.v6 {
			return l.index
		}
	}
	return 0
}

// v6Scoped reports whether IPv6 address a is bound to only with an
// interface, as Linux's __ipv6_addr_needs_scope_id: link-local unicast, and
// interface- and link-local multicast.
func v6Scoped(a []byte) bool {
	if a[0] == 0xff {
		s := a[1] & 0xf
		return s == 1 || s == 2
	}
	return a[0] == 0xfe && a[1]&0xc0 == 0x80
}

// group6Sockaddr is the sockaddr_in6 of IPv6 group a, port, on interface
// link (its scope id).
func group6Sockaddr(a [16]byte, port uint16, link uint32) []byte {
	b := make([]byte, 28)
	b[0] = lxAFInet6
	b[2], b[3] = byte(port>>8), byte(port)
	copy(b[8:24], a[:])
	b[24], b[25], b[26], b[27] = byte(link), byte(link>>8), byte(link>>16), byte(link>>24)
	return b
}

// vmcHasLink reports whether netns has interface index. The caller holds
// n.mu.
func (n *dnet) vmcHasLink(netns, index int32) bool {
	for _, l := range n.vmcLinks(netns) {
		if l.index == index {
			return true
		}
	}
	return false
}

// mcReq reads an ip_mreq or ip_mreqn: the group, and the interface by
// address or index.
func mcReq(b []byte) (group, addr uint32, index int32, e lxErrno) {
	if len(b) < 8 {
		return 0, 0, 0, lxEINVAL
	}
	group, addr = beIP(b[0:4]), beIP(b[4:8])
	if len(b) >= 12 {
		index = int32(le32(b[8:12]))
	}
	if !multicast4(group) {
		return 0, 0, 0, lxEINVAL
	}
	return group, addr, index, 0
}

// mcIfReq reads IP_MULTICAST_IF's value as Linux does: an in_addr, an
// ip_mreq (its interface) or an ip_mreqn (its address and index).
func mcIfReq(b []byte) (addr uint32, index int32, e lxErrno) {
	if len(b) < 4 {
		return 0, 0, lxEINVAL
	}
	addr = beIP(b[0:4])
	if len(b) >= 8 {
		addr = beIP(b[4:8])
	}
	if len(b) >= 12 {
		index = int32(le32(b[8:12]))
	}
	return addr, index, 0
}

// mcEdit joins m (or, drop, leaves it) in a socket's list, as Linux answers:
// EADDRINUSE for a group joined there already, EADDRNOTAVAIL to leave one
// not joined, ENOBUFS past igmp_max_memberships.
func mcEdit(list []mcMember, m mcMember, drop bool) ([]mcMember, lxErrno) {
	at := -1
	for i, x := range list {
		if x == m {
			at = i
		}
	}
	switch {
	case drop && at < 0:
		return list, lxEADDRNOTAVAIL
	case drop:
		return append(list[:at], list[at+1:]...), 0
	case at >= 0:
		return list, lxEADDRINUSE
	case len(list) >= mcMaxMemberships:
		return list, lxENOBUFS
	}
	return append(list, m), 0
}

// mc6Edit is mcEdit for IPv6 groups.
func mc6Edit(list []mc6Member, m mc6Member, drop bool) ([]mc6Member, lxErrno) {
	at := -1
	for i, x := range list {
		if x == m {
			at = i
		}
	}
	switch {
	case drop && at < 0:
		return list, lxEADDRNOTAVAIL
	case drop:
		return append(list[:at], list[at+1:]...), 0
	case at >= 0:
		return list, lxEADDRINUSE
	}
	return append(list, m), 0
}

// mcDevAdd counts a socket's membership in (delta 1) or out of (delta -1)
// the groups interface k.link of k.netns took in. The caller holds n.mu.
func (n *dnet) mcDevAdd(k mcDevKey, delta int) {
	if n.mcDev == nil {
		n.mcDev = map[mcDevKey]int{}
	}
	if n.mcDev[k] += delta; n.mcDev[k] <= 0 {
		delete(n.mcDev, k)
	}
}

// vmcLink6 resolves an IPv6 membership's interface in netns: by index, else
// the IPv6 multicast route's (vmcRoute6); 0 when there is none. The caller
// holds n.mu.
func (n *dnet) vmcLink6(netns, index int32) int32 {
	if index == 0 {
		return n.vmcRoute6(netns)
	}
	for _, l := range n.vmcLinks(netns) {
		if l.index == index {
			return index
		}
	}
	return 0
}

// vmcOut is the interface a datagram of netns to group leaves by (index, or
// the route's when 0), whether it is a loopback one, and the address it is
// sent from; link 0 when there is none. The caller holds n.mu.
func (n *dnet) vmcOut(netns, index int32, group uint32) (link int32, lo bool, src uint32) {
	link = index
	if link == 0 {
		link = n.vmcRoute(netns, group)
	}
	for _, l := range n.vmcLinks(netns) {
		if l.index == link {
			lo = l.loop
			if len(l.addrs) > 0 {
				src = l.addrs[0]
			}
		}
	}
	if src == 0 {
		src = 127<<24 | 1
	}
	return link, lo, src
}

// vmcLinkPeers is the interfaces in other namespaces on the same link as
// interface link of netns (the bridge it reaches through its veth's host
// end, or for a bridge in the host namespace, the bridge itself), each with
// group. The caller holds n.mu.
func (n *dnet) vmcLinkPeers(netns int32, link int32, group uint32) []mcDevKey {
	host := n.nets[0]
	if host == nil {
		return nil
	}
	var bridge int32
	if netns == 0 {
		if l := host.byIndex(link); l != nil && l.kind == "bridge" {
			bridge = link
		}
	} else if m := n.nets[netns]; m != nil {
		if l := m.byIndex(link); l != nil && l.peer != 0 {
			if he := host.byIndex(l.peer); he != nil {
				bridge = he.master
			}
		}
	}
	if bridge == 0 {
		return nil
	}
	var out []mcDevKey
	if netns != 0 {
		out = append(out, mcDevKey{0, bridge, group})
	}
	for ns, m := range n.nets {
		if ns == 0 || ns == netns {
			continue
		}
		for _, l := range m.links {
			if l.peer == 0 {
				continue
			}
			if he := host.byIndex(l.peer); he != nil && he.master == bridge {
				out = append(out, mcDevKey{ns, l.index, group})
			}
		}
	}
	return out
}

// keptDefault is Linux's value of an option a socket keeps for itself
// (optNoop, and vloKept on a vnet socket) before it is set: the kernel's
// defaults, with its default sysctls. An option it does not list starts at 0.
var keptDefault = map[[2]int]int32{
	{lxSolIP, 2}:                64,   // IP_TTL (net.ipv4.ip_default_ttl)
	{lxSolIP, 10}:               1,    // IP_MTU_DISCOVER: IP_PMTUDISC_WANT
	{lxSolIP, 33}:               1,    // IP_MULTICAST_TTL
	{lxSolIP, 34}:               1,    // IP_MULTICAST_LOOP
	{lxSolIP, lxIPMulticastAll}: 1,    // IP_MULTICAST_ALL
	{lxSolIPv6, 16}:             64,   // IPV6_UNICAST_HOPS
	{lxSolIPv6, 18}:             1,    // IPV6_MULTICAST_HOPS
	{lxSolIPv6, 19}:             1,    // IPV6_MULTICAST_LOOP
	{lxSolIPv6, 23}:             1,    // IPV6_MTU_DISCOVER: IPV6_PMTUDISC_WANT
	{lxSolIPv6, 29}:             1,    // IPV6_MULTICAST_ALL
	{lxSolTCP, 2}:               536,  // TCP_MAXSEG, unconnected (TCP_MSS_DEFAULT)
	{lxSolTCP, 4}:               7200, // TCP_KEEPIDLE (net.ipv4.tcp_keepalive_time)
	{lxSolTCP, 5}:               75,   // TCP_KEEPINTVL
	{lxSolTCP, 6}:               9,    // TCP_KEEPCNT
	{lxSolTCP, 7}:               6,    // TCP_SYNCNT (net.ipv4.tcp_syn_retries)
	{lxSolTCP, 8}:               60,   // TCP_LINGER2 (net.ipv4.tcp_fin_timeout)
	{lxSolTCP, 12}:              1,    // TCP_QUICKACK
	{lxSolTCP, 25}:              -1,   // TCP_NOTSENT_LOWAT (net.ipv4.tcp_notsent_lowat)
}

// prefixMask returns the IPv4 netmask for a prefix length.
func prefixMask(p uint8) uint32 {
	switch {
	case p == 0:
		return 0
	case p >= 32:
		return 0xffffffff
	}
	return ^uint32(0) << (32 - p)
}
