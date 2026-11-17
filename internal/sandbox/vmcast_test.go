//go:build darwin || linux

package sandbox

import (
	"slices"
	"testing"
)

// TestVmcTopology: a multicast membership's or send's interface is found as
// Linux finds it (by index, by address, by the route to the group), and a
// datagram out of a container's interface reaches the namespaces on the same
// bridge, not those on another.
func TestVmcTopology(t *testing.T) {
	n := &dnet{}
	host := n.linkModel(0)
	br := &netLink{index: host.nextIdx, name: "br0", kind: "bridge"}
	host.nextIdx++
	host.links = append(host.links, br)
	br1 := &netLink{index: host.nextIdx, name: "br1", kind: "bridge"}
	host.nextIdx++
	host.links = append(host.links, br1)
	attach(n, host, br.index, 2, v4(10, 88, 0, 2))
	attach(n, host, br.index, 3, v4(10, 88, 0, 3))
	attach(n, host, br1.index, 4, v4(10, 99, 0, 2))
	eth := func(ns int32) *netLink { return n.nets[ns].byName("eth0") }

	group := ip4(224, 0, 0, 251)
	m2 := n.nets[2]
	m2.routes = append(m2.routes, netRoute{family: lxAFInet, gateway: v4(10, 88, 0, 1)}) // default via the gateway, no oif
	if got := n.vmcRoute(2, group); got != eth(2).index {
		t.Errorf("route to the group: %d, want eth0 %d", got, eth(2).index)
	}
	if got := n.vmcRoute(3, group); got != 0 {
		t.Errorf("a namespace without a default route found one: %d", got)
	}
	if got := n.vmcFind(2, 0, ip4(10, 88, 0, 2), group); got != eth(2).index {
		t.Errorf("by address: %d", got)
	}
	if got := n.vmcFind(2, 99, 0, group); got != 0 {
		t.Errorf("by a missing index: %d", got)
	}
	if got := n.vmcFind(3, eth(3).index, 0, group); got != eth(3).index {
		t.Errorf("by index: %d", got)
	}

	peers := n.vmcLinkPeers(2, eth(2).index, group)
	if !slices.Contains(peers, mcDevKey{3, eth(3).index, group}) || !slices.Contains(peers, mcDevKey{0, br.index, group}) {
		t.Errorf("peers of A's eth0 %v lack C or the bridge", peers)
	}
	for _, p := range peers {
		if p.netns == 4 || p.netns == 2 {
			t.Errorf("peers of A's eth0 include %v", p)
		}
	}
	if peers := n.vmcLinkPeers(4, eth(4).index, group); len(peers) != 1 || peers[0] != (mcDevKey{0, br1.index, group}) {
		t.Errorf("peers of D's eth0 on its own bridge: %v", peers)
	}

	if got := n.vmcRoute6(2); got != 0 {
		t.Errorf("an IPv6 route on an interface with no IPv6 address: %d", got)
	}
	e := eth(2)
	e.flags |= lxIffUp
	e.addrs = append(e.addrs, netAddr{family: lxAFInet6, ip: make([]byte, 16), prefix: 64})
	if got := n.vmcRoute6(2); got != e.index {
		t.Errorf("IPv6 multicast route: %d, want %d", got, e.index)
	}
}
