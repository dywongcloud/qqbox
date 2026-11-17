//go:build linux

package sandbox

import (
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestPortPublishEndToEnd drives the whole generic -p path: a DNAT rule in the
// model (the real native-nft bytes: host 8080 -> 10.88.0.5:80) is realized into
// a host listener by reconcilePublish, and a connection to that host port is
// relayed to a container listener registered on the virtual network. No netavark
// recognition is involved; the publication comes from the firewall model.
func TestPortPublishEndToEnd(t *testing.T) {
	dir := t.TempDir()
	n := &dnet{dir: dir, vports: map[vkey]*vport{}, nftns: map[int32]*nftState{}}

	// A container listener on the virtual network at 10.88.0.5:80, backed by a
	// host unix socket at its vnet path and marked listening (as vlisten/vbind do).
	ctrIP := uint32(10<<24 | 88<<16 | 0<<8 | 5)
	ctrPort := uint16(80)
	k := vkey{vnetGlobal, ctrIP, ctrPort, false}
	lfd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	defer unix.Close(lfd)
	vp := &vport{path: vPath(dir, k)}
	if err := n.bindUnix(lfd, vp.path); err != nil {
		t.Fatalf("bindUnix: %v", err)
	}
	if err := unix.Listen(lfd, 8); err != nil {
		t.Fatalf("listen: %v", err)
	}
	vp.listening = true
	n.vports[k] = vp

	// The container's server: accept one connection and echo "pong" for "ping".
	go func() {
		c, _, err := unix.Accept(lfd)
		if err != nil {
			return
		}
		defer unix.Close(c)
		b := make([]byte, 8)
		m, _ := unix.Read(c, b)
		if m == 4 && string(b[:4]) == "ping" {
			unix.Write(c, []byte("pong"))
		}
	}()

	// The firewall model for the host namespace, holding the real DNAT rule.
	n.nftns[0] = &nftState{tables: []*nftTable{{
		family: 2, name: "nat",
		chains: []*nftChain{{name: "pre", rules: []*nftRule{{exprs: unhex(t, dnatNativeHex)}}}},
	}}}

	n.mu.Lock()
	n.reconcilePublish(0)
	published := len(n.pubs[0])
	n.mu.Unlock()
	if published == 0 {
		t.Skip("host port 8080 unavailable on this machine")
	}

	// A host connection to the published port must reach the container listener.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8080", 3*time.Second)
	if err != nil {
		t.Fatalf("dial published port: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := make([]byte, 8)
	m, err := conn.Read(r)
	if err != nil || string(r[:m]) != "pong" {
		t.Fatalf("got %q err %v; want pong", string(r[:m]), err)
	}

	// Removing the rule and reconciling again closes the host listener.
	n.mu.Lock()
	n.nftns[0].tables[0].chains[0].rules = nil
	n.reconcilePublish(0)
	left := len(n.pubs[0])
	n.mu.Unlock()
	if left != 0 {
		t.Fatalf("publication not torn down: %d left", left)
	}
}
