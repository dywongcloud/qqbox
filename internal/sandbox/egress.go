package sandbox

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// egress is a sandbox's outbound network policy (Config.AllowDomains,
// AllowCIDRs, AllowPorts) and the addresses the guest has resolved through
// the supervisor's DNS proxy, which a domain allowlist then lets it connect to.
//
// The supervisor is the guest's DNS resolver: each backend intercepts the
// guest's datagrams to any :53 nameserver and answers them with answer, which
// resolves on the host (so the host's resolver config applies) and enforces
// the domain allowlist. That is the policy chokepoint plain pass-through
// sockets lack.
type egress struct {
	allowDomains []string     // empty = all
	allowCIDRs   []*net.IPNet // networks the guest may dial without resolving
	allowPorts   []int        // destination ports allowed (empty = all)

	mu     sync.Mutex
	pinned map[string]time.Time // resolved IP -> expiry
}

// pinTTL bounds how long a resolved address stays connectable after its lookup,
// so a name that later points elsewhere (its DNS TTL is ~30s) cannot grant
// access forever. A connect is always preceded by a fresh resolve, which re-pins.
const pinTTL = 2 * time.Minute

func newEgress(cfg Config) *egress {
	e := &egress{allowDomains: cfg.AllowDomains, allowPorts: cfg.AllowPorts}
	for _, c := range cfg.AllowCIDRs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			e.allowCIDRs = append(e.allowCIDRs, n)
		}
	}
	return e
}

// internalIP reports whether ip is in a range the guest must not reach merely
// by resolving a name to it: loopback, RFC1918/ULA private, link-local (which
// includes the cloud metadata address 169.254.169.254), or the unspecified
// address. Such a target is reachable only when an explicit CIDR permits it,
// and the loopback never: the guest's is its own.
func internalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// domainAllowed reports whether name may be resolved. An empty allowlist
// permits everything; otherwise name must equal or be a subdomain of an entry.
func (e *egress) domainAllowed(name string) bool {
	if len(e.allowDomains) == 0 {
		return true
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	for _, d := range e.allowDomains {
		d = strings.ToLower(strings.TrimSuffix(d, "."))
		if name == d || strings.HasSuffix(name, "."+d) {
			return true
		}
	}
	return false
}

// pin records an IP the guest resolved through us, so a later connect to it
// is permitted under the egress policy.
func (e *egress) pin(ip string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if e.pinned == nil {
		e.pinned = map[string]time.Time{}
	}
	if len(e.pinned) >= 8192 { // bound growth: sweep expired entries
		for k, exp := range e.pinned {
			if now.After(exp) {
				delete(e.pinned, k)
			}
		}
	}
	e.pinned[ip] = now.Add(pinTTL)
}

// connectAllowed reports whether the egress policy permits connecting to
// ip:port on the host's network. The loopback (and 0.0.0.0, which Linux routes
// to localhost) never: it reaches every unauthenticated service on the host,
// and the guest's loopback is its own, served before the policy is asked. The
// port allowlist (if any) applies next. For the address: with no domain or
// CIDR allowlist everything is permitted; otherwise a configured CIDR, or an
// address previously resolved through our DNS.
func (e *egress) connectAllowed(ip net.IP, port int) bool {
	if ip.IsLoopback() || ip.IsUnspecified() {
		return false
	}
	if len(e.allowPorts) > 0 {
		ok := false
		for _, p := range e.allowPorts {
			if p == port {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(e.allowDomains) == 0 && len(e.allowCIDRs) == 0 {
		return true
	}
	for _, n := range e.allowCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	exp, ok := e.pinned[ip.String()]
	return ok && time.Now().Before(exp)
}

// answer builds the reply to a DNS query datagram per the policy, resolving
// A/AAAA questions with lookup (which may block). It builds the reply with
// dnsmessage rather than assembling wire bytes by hand. nil means the query
// was unparseable and gets no reply.
func (e *egress) answer(query []byte, lookup func(network, name string) ([]net.IP, dnsmessage.RCode)) []byte {
	var p dnsmessage.Parser
	hdr, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	name := strings.TrimSuffix(q.Name.String(), ".")

	if !e.domainAllowed(name) {
		h := dnsmessage.Header{ID: hdr.ID, Response: true, RCode: dnsmessage.RCodeRefused}
		rb := dnsmessage.NewBuilder(nil, h)
		rb.StartQuestions()
		rb.Question(q)
		out, _ := rb.Finish()
		return out
	}
	// Resolve before building the reply so it carries the resolver's real result
	// code: a lookup failure must be SERVFAIL and a missing name NXDOMAIN, not an
	// empty NOERROR the guest's resolver would cache as "exists, no such address".
	rcode := dnsmessage.RCodeSuccess
	var ips []net.IP
	network := ""
	switch q.Type {
	case dnsmessage.TypeA:
		network = "ip4"
	case dnsmessage.TypeAAAA:
		network = "ip6"
	}
	if network != "" {
		ips, rcode = lookup(network, name)
	}

	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: hdr.ID, Response: true, RecursionAvailable: true,
		RecursionDesired: hdr.RecursionDesired, RCode: rcode,
	})
	b.EnableCompression()
	b.StartQuestions()
	b.Question(q)
	b.StartAnswers()

	if rcode == dnsmessage.RCodeSuccess {
		for _, ip := range ips {
			// Resolving a name to an internal address does not grant access to
			// it (DNS rebinding / SSRF to the host or cloud metadata): it is only
			// reachable through an explicit CIDR, the loopback never. The answer
			// still carries the address, so split-horizon names keep working.
			if !internalIP(ip) {
				e.pin(ip.String())
			}
			rh := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 30}
			if v4 := ip.To4(); v4 != nil && q.Type == dnsmessage.TypeA {
				var a [4]byte
				copy(a[:], v4)
				b.AResource(rh, dnsmessage.AResource{A: a})
			} else if q.Type == dnsmessage.TypeAAAA && ip.To4() == nil {
				var a [16]byte
				copy(a[:], ip.To16())
				b.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: a})
			}
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// serveStream answers DNS over a stream connection: 2-byte length-framed
// queries, each answered by the policy in the same framing.
func (e *egress) serveStream(c net.Conn) {
	defer c.Close()
	var hdr [2]byte
	for {
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return
		}
		q := make([]byte, int(hdr[0])<<8|int(hdr[1]))
		if _, err := io.ReadFull(c, q); err != nil {
			return
		}
		reply := e.answer(q, hostLookup)
		if reply == nil {
			return
		}
		out := append([]byte{byte(len(reply) >> 8), byte(len(reply))}, reply...)
		if _, err := c.Write(out); err != nil {
			return
		}
	}
}

// hostLookup resolves name on the host, giving up after five seconds. It
// reports the DNS result code so the reply distinguishes a real negative answer
// (NXDOMAIN) and a resolver failure (SERVFAIL) from a successful empty result --
// the guest's resolver treats each very differently.
func hostLookup(network, name string) ([]net.IP, dnsmessage.RCode) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, network, name)
	if err != nil {
		var de *net.DNSError
		if errors.As(err, &de) && de.IsNotFound {
			return nil, dnsmessage.RCodeNameError // NXDOMAIN
		}
		return nil, dnsmessage.RCodeServerFailure // SERVFAIL (timeout, refused, ...)
	}
	return ips, dnsmessage.RCodeSuccess
}

// Linux address families, as the guest writes them in a sockaddr.
const (
	lxAFUnix  = 1
	lxAFInet  = 2
	lxAFInet6 = 10
)
