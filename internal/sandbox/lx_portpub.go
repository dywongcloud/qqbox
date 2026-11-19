//go:build darwin || linux

package sandbox

import "golang.org/x/sys/unix"

// -p port publishing, realized from the firewall. A container runtime publishes
// a container port on the host by installing a DNAT rule (host:P -> container
// addr:port) in the host namespace's nftables ruleset. qqbox has no real host
// datapath into a container -- the supervisor is the host side of the virtual
// network -- so it realizes each such rule by listening on the real host port
// and relaying an accepted connection to the container's listener on the virtual
// network. The mapping is read from the firewall the runtime installs, never
// from recognizing the runtime; the extraction is ruleDNAT in lx_nfteval.go.

// hostPub is one realized publication: the host listener fd and the
// virtual-network endpoint it relays to.
type hostPub struct {
	fd      int
	dstIP   uint32
	dstPort uint16
}

// dnatPubs returns the TCP host-port redirects the namespace's DNAT rules
// describe, keyed by host port. Caller holds n.mu.
func (s *nftState) dnatPubs() map[uint16]hostPub {
	out := map[uint16]hostPub{}
	for _, t := range s.tables {
		for _, c := range t.chains {
			for _, r := range c.rules {
				if hp, da, dp, ok := ruleDNAT(r); ok {
					out[hp] = hostPub{dstIP: da, dstPort: dp}
				}
			}
		}
	}
	return out
}

// reconcilePublish brings the host listeners for netns into line with the DNAT
// rules its firewall now holds: a listener is opened for each newly published
// port and closed when its rule is removed or retargeted. Caller holds n.mu.
func (n *dnet) reconcilePublish(netns int32) {
	s := n.nftns[netns]
	if s == nil {
		return
	}
	desired := s.dnatPubs()
	if n.pubs == nil {
		n.pubs = map[int32]map[uint16]hostPub{}
	}
	cur := n.pubs[netns]
	if cur == nil {
		cur = map[uint16]hostPub{}
		n.pubs[netns] = cur
	}
	for hp, pub := range cur {
		if d, ok := desired[hp]; !ok || d.dstIP != pub.dstIP || d.dstPort != pub.dstPort {
			unix.Close(pub.fd)
			delete(cur, hp)
		}
	}
	for hp, d := range desired {
		if _, ok := cur[hp]; ok {
			continue
		}
		fd, err := listenHost(hp)
		if err != nil {
			continue // the host port is unavailable; leave it unpublished
		}
		cur[hp] = hostPub{fd: fd, dstIP: d.dstIP, dstPort: d.dstPort}
		go n.publishAccept(fd, d.dstIP, d.dstPort)
	}
}

// listenHost opens a TCP listener on all host addresses at port.
func listenHost(port uint16) (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		return -1, err
	}
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	if err := unix.Bind(fd, &unix.SockaddrInet4{Port: int(port)}); err != nil {
		unix.Close(fd)
		return -1, err
	}
	if err := unix.Listen(fd, 128); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// publishAccept relays each inbound connection on a published host port to the
// container's listener on the virtual network, until the listener is closed.
func (n *dnet) publishAccept(lfd int, dstIP uint32, dstPort uint16) {
	for {
		hc, _, err := unix.Accept(lfd)
		if err != nil {
			return // the listener was closed (reconcile or run teardown)
		}
		n.mu.Lock()
		path, ok := n.vlistener(vnetGlobal, dstIP, dstPort, false, vkey{})
		n.mu.Unlock()
		if !ok {
			unix.Close(hc) // nothing listening in the container yet
			continue
		}
		uc, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err != nil {
			unix.Close(hc)
			continue
		}
		if err := n.viaShort(path, func(sa *unix.SockaddrUnix) error { return unix.Connect(uc, sa) }); err != nil {
			unix.Close(hc)
			unix.Close(uc)
			continue
		}
		go relayStream(hc, uc)
	}
}
