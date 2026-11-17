//go:build darwin || linux || windows

package sandbox

import (
	"net"
	"testing"
)

func nlU32(v uint32) []byte { b := make([]byte, 4); put32(b, v); return b }

func ifinfo(index int32, flags, change uint32, attrs ...[]byte) []byte {
	b := make([]byte, 16) // struct ifinfomsg
	put32(b[4:], uint32(index))
	put32(b[8:], flags)
	put32(b[12:], change)
	for _, a := range attrs {
		b = append(b, a...)
	}
	return b
}

func ifaddr(fam int, prefix byte, index int32, attrs ...[]byte) []byte {
	b := []byte{byte(fam), prefix, 0, 0, 0, 0, 0, 0} // struct ifaddrmsg
	put32(b[4:], uint32(index))
	for _, a := range attrs {
		b = append(b, a...)
	}
	return b
}

func rtmsgBody(fam int, dstLen byte, attrs ...[]byte) []byte {
	b := []byte{byte(fam), dstLen, 0, 0, rtTableMain, rtProtoStatic, 0, rtnUnicast, 0, 0, 0, 0}
	for _, a := range attrs {
		b = append(b, a...)
	}
	return b
}

// parseLinks maps interface name -> flags from RTM_NEWLINK dump datagrams.
func parseLinks(msgs [][]byte) map[string]uint32 {
	out := map[string]uint32{}
	for _, m := range msgs {
		body := m[nlmsgHdrLen:]
		at := parseNlAttrs(body[16:])
		out[cstr(at[iflaIfname])] = le32(body[8:])
	}
	return out
}

// parseAddrs maps "ip" -> prefix from RTM_NEWADDR dump datagrams.
func parseAddrs(msgs [][]byte) map[string]byte {
	out := map[string]byte{}
	for _, m := range msgs {
		body := m[nlmsgHdrLen:]
		at := parseNlAttrs(body[8:])
		out[net.IP(at[ifaAddress]).String()] = body[1]
	}
	return out
}

// parseLinkAttrs pulls the name/master/peer/kind out of an RTM_NEWLINK message.
func parseLinkAttrs(msg []byte) (name string, master, link int32, kind string) {
	body := msg[nlmsgHdrLen:]
	at := parseNlAttrs(body[16:])
	name = cstr(at[iflaIfname])
	if m := at[iflaMaster]; len(m) >= 4 {
		master = int32(le32(m))
	}
	if l := at[iflaLink]; len(l) >= 4 {
		link = int32(le32(l))
	}
	if li := at[iflaLinkinfo]; li != nil {
		kind = cstr(parseNlAttrs(li)[iflaInfoKind])
	}
	return
}

// TestNetModelLinkAttrs covers the link attributes netavark depends on: a bridge
// with a kind, a veth host end enslaved to it (IFLA_MASTER) with a peer index
// (IFLA_LINK), and a rename (SETLINK by index carrying IFLA_IFNAME).
func TestNetModelLinkAttrs(t *testing.T) {
	g := &dguest{}
	m := seedNetns()
	if e := g.nlConfigure(m, rtmNewlink, ifinfo(0, 0, 0,
		rtattr(iflaIfname, append([]byte("br0"), 0)),
		rtattr(iflaLinkinfo, rtattr(iflaInfoKind, append([]byte("bridge"), 0))))); e != 0 {
		t.Fatalf("newlink br0: errno %d", e)
	}
	br := m.byName("br0")
	if br == nil || br.kind != "bridge" {
		t.Fatalf("br0 not a bridge: %+v", br)
	}
	if e := g.nlConfigure(m, rtmNewlink, ifinfo(0, 0, 0,
		rtattr(iflaIfname, append([]byte("vethH"), 0)),
		rtattr(iflaAddress, []byte{2, 0, 0, 0, 0, 9}),
		rtattr(iflaMaster, nlU32(uint32(br.index))),
		rtattr(iflaLink, nlU32(42)),
		rtattr(iflaLinkinfo, rtattr(iflaInfoKind, append([]byte("veth"), 0))))); e != 0 {
		t.Fatalf("newlink vethH: errno %d", e)
	}
	found := false
	for _, msg := range g.nlModelLinks(m, 1, 1) {
		if name, master, link, kind := parseLinkAttrs(msg); name == "vethH" {
			found = true
			if master != br.index || link != 42 || kind != "veth" {
				t.Fatalf("vethH attrs: master=%d link=%d kind=%s", master, link, kind)
			}
		}
	}
	if !found {
		t.Fatal("vethH missing from link dump")
	}
	// Rename vethH -> eth0.
	vh := m.byName("vethH")
	if e := g.nlConfigure(m, rtmSetlink, ifinfo(vh.index, 0, 0, rtattr(iflaIfname, append([]byte("eth0"), 0)))); e != 0 {
		t.Fatalf("rename: errno %d", e)
	}
	if m.byName("eth0") == nil || m.byName("vethH") != nil {
		t.Fatal("rename did not take effect")
	}
}

func ifinfoPeer(name string, mac []byte) []byte {
	b := make([]byte, 16) // struct ifinfomsg
	b = append(b, rtattr(iflaIfname, append([]byte(name), 0))...)
	b = append(b, rtattr(iflaAddress, mac)...)
	return b
}

// TestNetModelVethPair: one RTM_NEWLINK carrying a VETH_INFO_PEER creates two
// cross-linked ends -- the primary in the sending namespace (here the host
// overlay), the peer in the target namespace.
func TestNetModelVethPair(t *testing.T) {
	n := &dnet{}
	m := n.linkModel(0) // host netns overlay
	const container int32 = 7
	peer := ifinfoPeer("eth0", []byte{2, 0, 0, 0, 0, 1})
	linkinfo := rtattr(iflaLinkinfo, append(
		rtattr(iflaInfoKind, append([]byte("veth"), 0)),
		rtattr(iflaInfoData, rtattr(vethInfoPeer, peer))...))
	body := ifinfo(0, 0, 0, rtattr(iflaMaster, nlU32(100)), linkinfo)

	pb, ok := vethPeer(body)
	if !ok {
		t.Fatal("vethPeer not detected")
	}
	n.createVeth(m, body, pb, container)

	if len(m.links) != 1 || m.links[0].kind != "veth" || m.links[0].master != 100 {
		t.Fatalf("host veth end wrong: %+v", m.links)
	}
	if m.links[0].index < virtBase {
		t.Fatalf("host veth end should take a high overlay index, got %d", m.links[0].index)
	}
	cm := n.linkModel(container)
	var pl *netLink
	for _, l := range cm.links {
		if l.name == "eth0" {
			pl = l
		}
	}
	if pl == nil {
		t.Fatal("peer eth0 not created in the container netns")
	}
	if m.links[0].peer != pl.index || pl.peer != m.links[0].index {
		t.Fatalf("veth ends not cross-linked: host.peer=%d peer.idx=%d peer.peer=%d host.idx=%d",
			m.links[0].peer, pl.index, pl.peer, m.links[0].index)
	}
}

func TestNetModelRTNL(t *testing.T) {
	g := &dguest{}
	m := seedNetns()
	if len(m.links) != 1 || m.links[0].name != "lo" || m.links[0].flags&lxIffUp != 0 {
		t.Fatalf("fresh netns should be lo-down-no-addr, got %+v", m.links[0])
	}

	// Bring lo up: the kernel then auto-assigns 127.0.0.1/8 and ::1/128.
	if e := g.nlConfigure(m, rtmSetlink, ifinfo(1, lxIffUp, lxIffUp)); e != 0 {
		t.Fatalf("setlink lo up: errno %d", e)
	}
	lo := m.byName("lo")
	if lo.flags&lxIffUp == 0 {
		t.Fatal("lo still down after setlink")
	}
	loAddrs := parseAddrs(g.nlModelAddrs(m, 1, 1, 0))
	if loAddrs["127.0.0.1"] != 8 || loAddrs["::1"] != 128 {
		t.Fatalf("lo up should auto-add loopback addrs, got %v", loAddrs)
	}
	// Re-adding 127.0.0.1 is idempotent, not a duplicate.
	if e := g.nlConfigure(m, rtmNewaddr, ifaddr(lxAFInet, 8, 1, rtattr(ifaAddress, net.IPv4(127, 0, 0, 1).To4()))); e != 0 {
		t.Fatalf("newaddr lo: errno %d", e)
	}
	if n := len(lo.addrs); n != 2 {
		t.Fatalf("lo should have exactly 127.0.0.1 and ::1, got %d addrs", n)
	}
	// Create eth0 (veth), up, with 10.0.0.2/24.
	linkinfo := rtattr(iflaLinkinfo, rtattr(iflaInfoKind, append([]byte("veth"), 0)))
	if e := g.nlConfigure(m, rtmNewlink, ifinfo(0, lxIffUp, lxIffUp, rtattr(iflaIfname, append([]byte("eth0"), 0)), linkinfo)); e != 0 {
		t.Fatalf("newlink eth0: errno %d", e)
	}
	eth := m.byName("eth0")
	if eth == nil || eth.index != 2 || eth.kind != "veth" || eth.flags&lxIffUp == 0 {
		t.Fatalf("eth0 not created as expected: %+v", eth)
	}
	if e := g.nlConfigure(m, rtmNewaddr, ifaddr(lxAFInet, 24, eth.index, rtattr(ifaAddress, net.IPv4(10, 0, 0, 2).To4()))); e != 0 {
		t.Fatalf("newaddr eth0: errno %d", e)
	}
	// Default route via 10.0.0.1 dev eth0.
	if e := g.nlConfigure(m, rtmNewroute, rtmsgBody(lxAFInet, 0, rtattr(rtaGateway, net.IPv4(10, 0, 0, 1).To4()), rtattr(rtaOif, nlU32(uint32(eth.index))))); e != 0 {
		t.Fatalf("newroute default: errno %d", e)
	}

	// Link dump reflects both interfaces, up.
	links := parseLinks(g.nlModelLinks(m, 1, 1))
	if len(links) != 2 || links["lo"]&lxIffUp == 0 || links["eth0"]&lxIffUp == 0 {
		t.Fatalf("link dump wrong: %v", links)
	}
	// Address dump reflects both addresses, with prefixes.
	addrs := parseAddrs(g.nlModelAddrs(m, 1, 1, 0))
	if addrs["127.0.0.1"] != 8 || addrs["10.0.0.2"] != 24 {
		t.Fatalf("addr dump wrong: %v", addrs)
	}
	// Family filter: only the loopback ::1 is IPv6.
	if n := len(g.nlModelAddrs(m, 1, 1, lxAFInet6)); n != 1 {
		t.Fatalf("v6 filter: want 1 (::1), got %d", n)
	}
	// Route dump has the one default route with the right gateway.
	routes := g.nlModelRoutes(m, 1, 1, 0)
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
	}
	rbody := routes[0][nlmsgHdrLen:]
	if rbody[1] != 0 { // dst_len 0 == default
		t.Fatalf("route not default: dst_len %d", rbody[1])
	}
	if gw := net.IP(parseNlAttrs(rbody[12:])[rtaGateway]).String(); gw != "10.0.0.1" {
		t.Fatalf("route gateway %s", gw)
	}

	// Deletions take effect.
	if e := g.nlConfigure(m, rtmDeladdr, ifaddr(lxAFInet, 8, 1, rtattr(ifaAddress, net.IPv4(127, 0, 0, 1).To4()))); e != 0 {
		t.Fatalf("deladdr lo: errno %d", e)
	}
	if _, ok := parseAddrs(g.nlModelAddrs(m, 1, 1, 0))["127.0.0.1"]; ok {
		t.Fatal("127.0.0.1 survived deladdr")
	}
	if e := g.nlConfigure(m, rtmDellink, ifinfo(eth.index, 0, 0)); e != 0 {
		t.Fatalf("dellink eth0: errno %d", e)
	}
	if len(parseLinks(g.nlModelLinks(m, 1, 1))) != 1 {
		t.Fatal("eth0 survived dellink")
	}

	// SETLINK of a missing interface and NEWADDR on a missing index fail.
	if e := g.nlConfigure(m, rtmSetlink, ifinfo(99, lxIffUp, lxIffUp)); e != lxENODEV {
		t.Fatalf("setlink missing: want ENODEV, got %d", e)
	}
	if e := g.nlConfigure(m, rtmNewaddr, ifaddr(lxAFInet, 24, 99, rtattr(ifaAddress, net.IPv4(1, 2, 3, 4).To4()))); e != lxENODEV {
		t.Fatalf("newaddr missing link: want ENODEV, got %d", e)
	}
}

// TestNetModelHostOverlay: the host netns (id 0) starts with an empty overlay at
// a high index base (real interfaces are reported live), a typed create (bridge)
// is the model's, and a mutation of a real host interface is not -- it is acked
// without touching the host.
func TestNetModelHostOverlay(t *testing.T) {
	n := &dnet{}
	m := n.linkModel(0)
	if len(m.links) != 0 || m.nextIdx != virtBase {
		t.Fatalf("host overlay should start empty at virtBase, got %d links nextIdx=%d", len(m.links), m.nextIdx)
	}
	g := &dguest{}
	if m.owns(rtmSetlink, ifinfo(0, lxIffUp, lxIffUp, rtattr(iflaIfname, append([]byte("eth0"), 0)))) {
		t.Fatal("SETLINK of an unmodelled host interface must not be owned")
	}
	br := ifinfo(0, 0, 0,
		rtattr(iflaIfname, append([]byte("cni0"), 0)),
		rtattr(iflaLinkinfo, rtattr(iflaInfoKind, append([]byte("bridge"), 0))))
	if !m.owns(rtmNewlink, br) {
		t.Fatal("a typed bridge create must be owned by the model")
	}
	if e := g.nlConfigure(m, rtmNewlink, br); e != 0 {
		t.Fatalf("create bridge: errno %d", e)
	}
	b := m.byName("cni0")
	if b == nil || b.index < virtBase || b.kind != "bridge" {
		t.Fatalf("bridge not created in the overlay at a high index: %+v", b)
	}
	if !m.owns(rtmNewaddr, ifaddr(lxAFInet, 24, b.index)) {
		t.Fatal("NEWADDR on the overlay bridge must be owned")
	}
	if m.owns(rtmNewaddr, ifaddr(lxAFInet, 24, 2)) {
		t.Fatal("NEWADDR on a real host interface index must not be owned")
	}
}

func TestParseNlAttrs(t *testing.T) {
	buf := append(rtattr(iflaIfname, append([]byte("eth0"), 0)), rtattr(iflaMtu, nlU32(1500))...)
	at := parseNlAttrs(buf)
	if cstr(at[iflaIfname]) != "eth0" {
		t.Fatalf("ifname %q", cstr(at[iflaIfname]))
	}
	if le32(at[iflaMtu]) != 1500 {
		t.Fatalf("mtu %d", le32(at[iflaMtu]))
	}
	// A truncated trailing attribute is ignored, not panicked on.
	if len(parseNlAttrs([]byte{8, 0})) != 0 {
		t.Fatal("truncated attr should yield nothing")
	}
}
