//go:build darwin || linux || windows

package sandbox

import (
	"net"
	"testing"
)

func v4(a, b, c, d byte) []byte { return []byte{a, b, c, d} }

// attach wires a container namespace to a bridge the way createVeth + NEWADDR do:
// a host-side veth end enslaved to the bridge, and a peer end (eth0) in the
// container namespace holding the address.
func attach(n *dnet, host *netModel, bridge, netns int32, ip []byte) {
	he := &netLink{index: host.nextIdx, kind: "veth", master: bridge}
	host.nextIdx++
	host.links = append(host.links, he)
	cm := n.linkModel(netns)
	peer := &netLink{index: cm.nextIdx, name: "eth0", kind: "veth", peer: he.index}
	cm.nextIdx++
	peer.addrs = append(peer.addrs, netAddr{family: lxAFInet, ip: ip, prefix: 16})
	cm.links = append(cm.links, peer)
	he.peer = peer.index
}

func TestNetReach(t *testing.T) {
	n := &dnet{}
	host := n.linkModel(0)
	br := &netLink{index: host.nextIdx, name: "br0", kind: "bridge"}
	host.nextIdx++
	br.addrs = append(br.addrs, netAddr{family: lxAFInet, ip: v4(10, 88, 0, 1), prefix: 16})
	host.links = append(host.links, br)
	br2 := &netLink{index: host.nextIdx, name: "br1", kind: "bridge"}
	host.nextIdx++
	host.links = append(host.links, br2)

	attach(n, host, br.index, 2, v4(10, 88, 0, 2))  // container A on br0
	attach(n, host, br.index, 3, v4(10, 88, 0, 3))  // container C on br0
	attach(n, host, br2.index, 4, v4(10, 99, 0, 2)) // container D on br1

	check := func(src int32, ip []byte, wantNS int32, wantOK bool) {
		t.Helper()
		if ns, ok := n.reach(src, ip); ns != wantNS || ok != wantOK {
			t.Fatalf("reach(%d, %v) = (%d, %v); want (%d, %v)", src, net.IP(ip), ns, ok, wantNS, wantOK)
		}
	}
	check(2, v4(10, 88, 0, 3), 3, true)  // A -> C: same bridge
	check(2, v4(10, 88, 0, 1), 0, true)  // A -> gateway: the bridge in the host ns
	check(0, v4(10, 88, 0, 2), 2, true)  // host -> a container it bridges
	check(2, v4(10, 99, 0, 2), 0, false) // A -> D: different bridge, isolated
	check(2, v4(8, 8, 8, 8), 0, false)   // external address: egress
	check(2, v4(127, 0, 0, 1), 2, true)  // loopback stays in the namespace
	check(2, v4(10, 88, 0, 2), 2, true)  // A -> itself
}
