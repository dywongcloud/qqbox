//go:build darwin || linux

package sandbox

import "golang.org/x/sys/unix"

// Multicast on vnet (lx_vmcast.go): a vnet socket's memberships, and the
// delivery of what it sends to a group across the namespaces vnet joins.

// vmcMembership joins (or, drop, leaves) the IPv4 group an ip_mreq or
// ip_mreqn b names, for d, a vnet socket.
func (g *dguest) vmcMembership(d *dfile, b []byte, drop bool) int64 {
	sk := d.sk
	if sk.typ == lxSockStream {
		return sErr(lxEPROTO)
	}
	group, addr, index, e := mcReq(b)
	if e != 0 {
		return sErr(e)
	}
	n := g.netState()
	n.mu.Lock()
	link := n.vmcFind(sk.netns, index, addr, group)
	n.mu.Unlock()
	if link == 0 && (!drop || (index == 0 && addr == 0)) {
		return sErr(lxENODEV)
	}
	sk.mu.Lock()
	defer sk.mu.Unlock()
	list, e := mcEdit(sk.mcast, mcMember{link, group}, drop)
	if e != 0 {
		return sErr(e)
	}
	sk.mcast, sk.mcNet = list, n
	delta := 1
	if drop {
		delta = -1
	}
	n.mu.Lock()
	n.mcDevAdd(mcDevKey{sk.netns, link, group}, delta)
	if vp := sk.vp; vp != nil && sk.vudp {
		vp.mc = append([]mcMember(nil), sk.mcast...)
	}
	n.mu.Unlock()
	return 0
}

// vmcMembership6 joins (or, drop, leaves) the IPv6 group an ipv6_mreq b
// names, for d, a vnet socket.
func (g *dguest) vmcMembership6(d *dfile, b []byte, drop bool) int64 {
	sk := d.sk
	if len(b) < 20 {
		return sErr(lxEINVAL)
	}
	if sk.typ == lxSockStream {
		return sErr(lxEPROTO)
	}
	var m mc6Member
	copy(m.group[:], b[:16])
	if m.group[0] != 0xff {
		return sErr(lxEINVAL)
	}
	n := g.netState()
	n.mu.Lock()
	m.link = n.vmcLink6(sk.netns, int32(le32(b[16:20])))
	n.mu.Unlock()
	if m.link == 0 {
		return sErr(lxENODEV)
	}
	sk.mu.Lock()
	defer sk.mu.Unlock()
	list, e := mc6Edit(sk.mcast6, m, drop)
	if e != 0 {
		return sErr(e)
	}
	sk.mcast6 = list
	return 0
}

// vmcSetIf sets IP_MULTICAST_IF for d, a vnet socket (mcIfReq): the
// interface by index, else by an address it holds; the wildcard clears it.
func (g *dguest) vmcSetIf(d *dfile, b []byte) int64 {
	sk := d.sk
	addr, index, e := mcIfReq(b)
	if e != 0 {
		return sErr(e)
	}
	var link int32
	if index != 0 || addr != 0 {
		n := g.netState()
		n.mu.Lock()
		link = n.vmcFind(sk.netns, index, addr, 0)
		n.mu.Unlock()
		if link == 0 {
			return sErr(lxEADDRNOTAVAIL)
		}
	}
	sk.mu.Lock()
	sk.mcIf, sk.mcIfAddr = link, addr
	sk.mu.Unlock()
	return 0
}

// vmcSetIf6 sets IPV6_MULTICAST_IF for d, a vnet socket: an interface that
// exists, or 0 for none, as Linux's do_ipv6_setsockopt takes it.
func (g *dguest) vmcSetIf6(d *dfile, b []byte) int64 {
	sk := d.sk
	if sk.typ == lxSockStream {
		return sErr(lxENOPROTOOPT)
	}
	if len(b) < 4 {
		return sErr(lxEINVAL)
	}
	v := int32(le32(b))
	if v != 0 {
		n := g.netState()
		n.mu.Lock()
		ok := n.vmcHasLink(sk.netns, v)
		n.mu.Unlock()
		if !ok {
			return sErr(lxENODEV)
		}
	}
	sk.mu.Lock()
	if sk.opts == nil {
		sk.opts = map[[2]int]int32{}
	}
	sk.opts[[2]int{lxSolIPv6, 17}] = v
	sk.mu.Unlock()
	return 0
}

// vgroup6 is the key ip standing for IPv6 group a bound to on interface link
// (0 when its scope takes none): the same for the same group and interface,
// and none the IPv6 addresses vnet keys (:: and ::1) have (vgroupKey). The
// caller holds n.mu.
func (n *dnet) vgroup6(a [16]byte, link uint32) uint32 {
	var k [20]byte
	copy(k[:], a[:])
	k[16], k[17], k[18], k[19] = byte(link), byte(link>>8), byte(link>>16), byte(link>>24)
	if ip, ok := n.group6[k]; ok {
		return ip
	}
	if n.group6 == nil {
		n.group6 = map[[20]byte]uint32{}
	}
	ip := 0xff000000 | uint32(len(n.group6)+1)
	n.group6[k] = ip
	return ip
}

// vgroupKey reports whether endpoint k is a multicast group's: a datagram
// sent from it comes from the address the route picks, as from a wildcard.
func vgroupKey(k vkey) bool {
	if k.v6 {
		return k.ip>>24 == 0xff
	}
	return multicast4(k.ip)
}

// vbindGroup6 binds d, a virtual datagram socket, to the IPv6 group raw
// names, as Linux's inet6_bind takes a multicast address: with an interface
// that exists (its scope id) when its scope needs one. d.sk.mu is held.
func (g *dguest) vbindGroup6(d *dfile, raw []byte) int64 {
	if _, e := inetSockaddr(raw, d.sk.family, false); e != 0 {
		return sErr(e)
	}
	var a [16]byte
	copy(a[:], raw[8:24])
	port := uint16(raw[2])<<8 | uint16(raw[3])
	link := le32(raw[24:28])
	if !v6Scoped(a[:]) {
		link = 0
	} else if link == 0 {
		return sErr(lxEINVAL)
	}
	n := g.netState()
	n.mu.Lock()
	ok := link == 0 || n.vmcHasLink(d.sk.netns, int32(link))
	ip := n.vgroup6(a, link)
	n.mu.Unlock()
	if !ok {
		return sErr(lxENODEV)
	}
	if e := g.vudpBind(d, vkey{d.sk.netns, ip, port, true}, 0); e != 0 {
		return sErr(e)
	}
	d.sk.name = group6Sockaddr(a, d.sk.vk.port, link)
	return 0
}

// vmcLeaveAll drops what s, closing, joined.
func (s *dsock) vmcLeaveAll() {
	n := s.mcNet
	if n == nil {
		return
	}
	n.mu.Lock()
	for _, m := range s.mcast {
		n.mcDevAdd(mcDevKey{s.netns, m.link, m.group}, -1)
	}
	n.mu.Unlock()
}

// optOr is sk's value of a Linux-only option it keeps, def when it was
// never set.
func optOr(sk *dsock, level, name int, def int32) int32 {
	sk.mu.Lock()
	defer sk.mu.Unlock()
	if v, ok := sk.opts[[2]int{level, name}]; ok {
		return v
	}
	return def
}

// vmcSend sends a datagram from d, a vnet socket, to group:port
// (lx_vmcast.go).
func (g *dguest) vmcSend(d *dfile, iovs [][2]uint64, group uint32, port uint16) int64 {
	sk := d.sk
	total := iovTotal(iovs)
	if total > 65507 {
		return sErr(lxEMSGSIZE)
	}
	n := g.netState()
	sk.mu.Lock()
	index := sk.mcIf
	sk.mu.Unlock()
	n.mu.Lock()
	link, lo, src := n.vmcOut(sk.netns, index, group)
	n.mu.Unlock()
	if link == 0 {
		return sErr(lxENETUNREACH)
	}
	if e := g.vudpAutobind(d, group, false); e != 0 {
		return sErr(e)
	}
	data, e := g.gather(iovs, 0, total)
	if e != 0 {
		return sErr(e)
	}
	fd, from := g.vsrcFdAt(d, hostFd(d.f), src, false)
	ttl := optOr(sk, lxSolIP, 33, keptDefault[[2]int{lxSolIP, 33}])
	loop := optOr(sk, lxSolIP, 34, keptDefault[[2]int{lxSolIP, 34}])
	n.mu.Lock()
	var targets []mcDevKey
	if loop != 0 || lo {
		targets = append(targets, mcDevKey{sk.netns, link, group})
	}
	if ttl > 0 && !lo {
		targets = append(targets, n.vmcLinkPeers(sk.netns, link, group)...)
	}
	var paths []string
	for _, t := range targets {
		if n.mcDev[t] == 0 {
			continue // the interface did not take the group in
		}
		for k, chain := range n.vudps {
			if k.scope != t.netns || k.port != port || !(k.ip == 0 || k.ip == group && !k.v6) {
				continue
			}
			for vp := chain; vp != nil; vp = vp.next {
				if k.v6 && vp.v6only || vp.peer.port != 0 && vp.peer != from {
					continue
				}
				joined := !vp.mcNoAll
				for _, m := range vp.mc {
					joined = joined || m == mcMember{t.link, group}
				}
				if joined {
					paths = append(paths, vp.path)
				}
			}
		}
	}
	n.mu.Unlock()
	for _, p := range paths {
		n.viaShort(p, func(sa *unix.SockaddrUnix) error { return unix.Sendto(fd, data, unix.MSG_DONTWAIT, sa) })
	}
	return int64(total)
}
