//go:build darwin || linux

package sandbox

import "testing"

// TestSyncIPNetns: a namespace's modelled non-loopback IPv4 addresses feed the
// vnet delivery map (replacing the netavark shim's registration), and loopback
// is left out.
func TestSyncIPNetns(t *testing.T) {
	n := &dnet{ipNetns: map[uint32]int32{}}
	m := seedNetns() // lo, up with 127.0.0.1 after a bring-up; here just lo
	eth := &netLink{index: 2, name: "eth0"}
	eth.addrs = append(eth.addrs, netAddr{family: lxAFInet, ip: []byte{10, 88, 0, 5}, prefix: 16})
	m.links = append(m.links, eth)

	n.syncIPNetns(5, m)
	if n.ipNetns[10<<24|88<<16|0<<8|5] != 5 {
		t.Fatalf("eth0 address not fed into ipNetns: %v", n.ipNetns)
	}
	if len(n.ipNetns) != 1 {
		t.Fatalf("loopback should not be registered; got %d entries", len(n.ipNetns))
	}
	// A re-sync after the address is removed clears the entry.
	eth.addrs = nil
	n.syncIPNetns(5, m)
	if len(n.ipNetns) != 0 {
		t.Fatalf("removed address should be dropped; got %d entries", len(n.ipNetns))
	}
}
