//go:build darwin || linux

package sandbox

import "testing"

func ip4(a, b, c, d uint32) uint32 { return a<<24 | b<<16 | c<<8 | d }

// vlistener routes a connect to the right listener: a virtual-network address
// resolves to its owning namespace's wildcard listener, loopback falls back to
// the namespace wildcard, and a loopback-only bind is not reachable externally.
func TestVlisten4(t *testing.T) {
	const srvNS, cliNS = int32(5), int32(7)
	srvIP := ip4(10, 88, 0, 6)
	n := &dnet{vports: map[vkey]*vport{}, ipNetns: map[uint32]int32{srvIP: srvNS}}
	// A server bound 0.0.0.0 in the server namespace.
	n.vports[vkey{srvNS, 0, 9000, false}] = &vport{path: "wild", listening: true}

	// A client connecting to the server's network address reaches it.
	if p, ok := n.vlistener(vnetGlobal, srvIP, 9000, false, vkey{}); !ok || p != "wild" {
		t.Fatalf("network connect: path=%q ok=%v, want wild/true", p, ok)
	}
	// An address not assigned to any namespace is unreachable.
	if _, ok := n.vlistener(vnetGlobal, ip4(10, 88, 0, 9), 9000, false, vkey{}); ok {
		t.Fatalf("unassigned address should be unreachable")
	}
	// Loopback from the same namespace reaches the wildcard listener.
	if p, ok := n.vlistener(srvNS, ip4(127, 0, 0, 1), 9000, false, vkey{}); !ok || p != "wild" {
		t.Fatalf("loopback fallback: path=%q ok=%v, want wild/true", p, ok)
	}
	// IPv6's loopback does not reach an IPv4 wildcard.
	if _, ok := n.vlistener(srvNS, ip4(127, 0, 0, 1), 9000, true, vkey{}); ok {
		t.Fatalf("::1 must not reach a listener on 0.0.0.0")
	}

	// A loopback-only bind must NOT be reachable at the network address.
	n2 := &dnet{vports: map[vkey]*vport{}, ipNetns: map[uint32]int32{srvIP: srvNS}}
	n2.vports[vkey{srvNS, ip4(127, 0, 0, 1), 9000, false}] = &vport{path: "lo", listening: true}
	if _, ok := n2.vlistener(vnetGlobal, srvIP, 9000, false, vkey{}); ok {
		t.Fatalf("loopback-only bind must not be reachable externally")
	}
	_ = cliNS
}

// TestVnetIPv6: IPv6's loopback and wildcard are addresses of their own, as
// Linux keeps them. An IPv6 wildcard takes IPv4 too unless IPV6_V6ONLY, which
// lets it share a port with an IPv4 wildcard; ::1 and 127.0.0.1 never
// overlap.
func TestVnetIPv6(t *testing.T) {
	const ns = int32(3)
	lo := ip4(127, 0, 0, 1)
	for _, c := range []struct {
		name         string
		held         vkey
		heldOnly     bool
		k            vkey
		v6only, want bool
	}{
		{"0.0.0.0 then [::] v6only", vkey{ns, 0, 80, false}, false, vkey{ns, 0, 80, true}, true, false},
		{"0.0.0.0 then [::]", vkey{ns, 0, 80, false}, false, vkey{ns, 0, 80, true}, false, true},
		{"[::] then 127.0.0.1", vkey{ns, 0, 80, true}, false, vkey{ns, lo, 80, false}, false, true},
		{"[::] v6only then 127.0.0.1", vkey{ns, 0, 80, true}, true, vkey{ns, lo, 80, false}, false, false},
		{"127.0.0.1 then ::1", vkey{ns, lo, 80, false}, false, vkey{ns, lo, 80, true}, false, false},
		{"0.0.0.0 then ::1", vkey{ns, 0, 80, false}, false, vkey{ns, lo, 80, true}, false, false},
		{"[::] v6only then ::1", vkey{ns, 0, 80, true}, true, vkey{ns, lo, 80, true}, false, true},
		{"::1 on another port", vkey{ns, lo, 81, true}, false, vkey{ns, lo, 80, true}, false, false},
	} {
		n := &dnet{ipNetns: map[uint32]int32{}}
		tab := map[vkey]*vport{c.held: {path: "held", listening: true, v6only: c.heldOnly}}
		if got := n.vtaken(tab, c.k, c.v6only, vshare{}, false); got != c.want {
			t.Errorf("%s: taken %v, want %v", c.name, got, c.want)
		}
	}

	n := &dnet{ipNetns: map[uint32]int32{}}
	n.vports = map[vkey]*vport{{ns, 0, 80, true}: {path: "dual", listening: true}}
	if p, ok := n.vlistener(ns, lo, 80, false, vkey{}); !ok || p != "dual" {
		t.Errorf("127.0.0.1 to a dual [::] listener: %q %v", p, ok)
	}
	if p, ok := n.vlistener(ns, lo, 80, true, vkey{}); !ok || p != "dual" {
		t.Errorf("::1 to a dual [::] listener: %q %v", p, ok)
	}
	n.vports[vkey{ns, 0, 80, true}].v6only = true
	if _, ok := n.vlistener(ns, lo, 80, false, vkey{}); ok {
		t.Errorf("127.0.0.1 reached a v6only [::] listener")
	}
	if got := vName(lxAFInet6, lo, 80, true); got[23] != 1 || got[20] != 0 {
		t.Errorf("vName(::1) = %v", got)
	}
}

// TestVnetShare: sockets share an endpoint as Linux lets them: datagram
// sockets both by SO_REUSEADDR or both by SO_REUSEPORT, stream sockets by
// SO_REUSEADDR only while the other does not listen; the newest sharer goes
// first, and leaving takes only its own place.
func TestVnetShare(t *testing.T) {
	const ns = int32(3)
	lo := ip4(127, 0, 0, 1)
	ra, rp := vshare{reuse: true}, vshare{reusePort: true}
	for _, c := range []struct {
		name          string
		held, sh      vshare
		listening     bool
		stream, taken bool
	}{
		{"udp reuseaddr both", ra, ra, false, false, false},
		{"udp reuseaddr one", vshare{}, ra, false, false, true},
		{"udp reuseport both", rp, rp, false, false, false},
		{"udp reuseport other uid", rp, vshare{reusePort: true, uid: 7}, false, false, true},
		{"udp reuseaddr over reuseport", rp, ra, false, false, true},
		{"tcp reuseaddr, not listening", ra, ra, false, true, false},
		{"tcp reuseaddr over a listener", ra, ra, true, true, true},
		{"tcp reuseport over a listener", rp, rp, true, true, false},
	} {
		n := &dnet{ipNetns: map[uint32]int32{}}
		tab := map[vkey]*vport{{ns, lo, 80, false}: {path: "held", listening: c.listening, share: c.held}}
		if got := n.vtaken(tab, vkey{ns, lo, 80, false}, false, c.sh, c.stream); got != c.taken {
			t.Errorf("%s: taken %v, want %v", c.name, got, c.taken)
		}
	}

	n := &dnet{dir: t.TempDir(), ipNetns: map[uint32]int32{}, vudps: map[vkey]*vport{}}
	k := vkey{ns, lo, 80, false}
	_, a, _ := n.vreserve(n.vudps, "vudp", k, false, ra, false)
	_, b, e := n.vreserve(n.vudps, "vudp", k, false, ra, false)
	if e != 0 || a.path == b.path {
		t.Fatalf("second sharer: %v, paths %q %q", e, a.path, b.path)
	}
	if p := n.vudpTarget(ns, lo, 80, false, vkey{}); p != b.path {
		t.Errorf("a datagram went to %q, not the newest sharer %q", p, b.path)
	}
	vremove(n.vudps, k, b)
	if p := n.vudpTarget(ns, lo, 80, false, vkey{}); p != a.path {
		t.Errorf("after the newest left, a datagram went to %q, not %q", p, a.path)
	}
	vremove(n.vudps, k, a)
	if len(n.vudps) != 0 {
		t.Errorf("reservations left: %v", n.vudps)
	}
}

// parseSock reads IPv6's own wildcard and loopback as such (v6), an
// IPv4-mapped address as the IPv4 one; global IPv6 is rejected.
func TestParseSock(t *testing.T) {
	in6 := func(addr [16]byte, port uint16) []byte {
		b := make([]byte, 28)
		b[0], b[1] = lxAFInet6, 0
		b[2], b[3] = byte(port>>8), byte(port)
		copy(b[8:24], addr[:])
		return b
	}
	var unspec, loop, mapped, global [16]byte
	loop[15] = 1
	mapped[10], mapped[11] = 0xff, 0xff
	mapped[12], mapped[13], mapped[14], mapped[15] = 10, 88, 0, 6
	global[0] = 0x20 // 2000::/3 global unicast

	cases := []struct {
		name   string
		raw    []byte
		ip     uint32
		v6, ok bool
	}{
		{"v4", sockaddrIn4(ip4(10, 88, 0, 6), 80), ip4(10, 88, 0, 6), false, true},
		{"::", in6(unspec, 80), 0, true, true},
		{"::1", in6(loop, 80), ip4(127, 0, 0, 1), true, true},
		{"::ffff:v4", in6(mapped, 80), ip4(10, 88, 0, 6), false, true},
		{"global", in6(global, 80), 0, true, false},
	}
	for _, c := range cases {
		ip, port, v6, ok := parseSock(c.raw)
		if ok != c.ok || (ok && (ip != c.ip || v6 != c.v6 || port != 80)) {
			t.Errorf("%s: ip=%d v6=%v ok=%v port=%d, want ip=%d v6=%v ok=%v", c.name, ip, v6, ok, port, c.ip, c.v6, c.ok)
		}
	}
}

// Only a namespace that was given an address on the network may egress; an
// isolated one (--network=none) may not.
func TestNetnsNetworked(t *testing.T) {
	n := &dnet{ipNetns: map[uint32]int32{ip4(10, 88, 0, 6): 5}}
	if !n.netnsNetworked(5) {
		t.Fatal("namespace 5 has an address, should be networked")
	}
	if n.netnsNetworked(9) {
		t.Fatal("namespace 9 has no address, should not be networked")
	}
}
