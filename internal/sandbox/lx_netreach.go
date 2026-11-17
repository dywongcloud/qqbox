//go:build darwin || linux || windows

package sandbox

import "net"

// Reachability over the modelled topology: given a connect from a source network
// namespace to a destination address, which namespace owns that address and can
// the source actually reach it. This is the data-plane question the control-plane
// model (links, addresses, veth peers, bridge membership) answers, and it
// replaces a flat global subnet in which any namespace could reach any address:
// two containers on different bridges must stay isolated, while two on the same
// bridge, and a container and its gateway, must connect.
//
// Reachability here is layer-2 domain membership: a namespace is on a bridge's
// domain if one of its interfaces is enslaved to that bridge (directly in the
// host namespace, or through a veth peer whose host end is). Two namespaces reach
// each other when they share a domain. Routing through a gateway to other domains
// is a later concern; direct same-subnet delivery and the gateway itself are what
// a default bridge network needs.

// ownerOf returns the namespace and link that hold address ip. Caller holds n.mu.
func (n *dnet) ownerOf(ip []byte) (int32, *netLink, bool) {
	for ns, m := range n.nets {
		for _, l := range m.links {
			for _, a := range l.addrs {
				if string(a.ip) == string(ip) {
					return ns, l, true
				}
			}
		}
	}
	return 0, nil, false
}

// bridgesOf returns the bridge indices (in the host namespace's model) whose
// layer-2 domain netns belongs to: every bridge in the host namespace itself,
// or, for a container, the bridge each of its veth ends reaches through its host
// peer. Caller holds n.mu.
func (n *dnet) bridgesOf(netns int32) []int32 {
	host := n.nets[0]
	var out []int32
	if netns == 0 {
		if host != nil {
			for _, l := range host.links {
				if l.kind == "bridge" {
					out = append(out, l.index)
				}
			}
		}
		return out
	}
	m := n.nets[netns]
	if m == nil || host == nil {
		return out
	}
	for _, l := range m.links {
		if l.peer == 0 {
			continue
		}
		if he := host.byIndex(l.peer); he != nil && he.master != 0 {
			out = append(out, he.master)
		}
	}
	return out
}

func sharesBridge(a, b []int32) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// reach resolves a connect from srcNetns to dstIP to the namespace that owns the
// address and is reachable from the source through the modelled topology. ok is
// false when the address is not a reachable local virtual address -- either it is
// owned by an unreachable (isolated) namespace, or by none, in which case it is
// external and takes the egress path. Caller holds n.mu.
func (n *dnet) reach(srcNetns int32, dstIP []byte) (int32, bool) {
	if net.IP(dstIP).IsLoopback() {
		return srcNetns, true // loopback stays within the namespace
	}
	owner, _, ok := n.ownerOf(dstIP)
	if !ok {
		return 0, false // not a local address: external egress
	}
	if owner == srcNetns {
		return owner, true // a namespace reaching its own address
	}
	if sharesBridge(n.bridgesOf(srcNetns), n.bridgesOf(owner)) {
		return owner, true
	}
	return 0, false // owned, but in an unreachable (isolated) namespace
}
