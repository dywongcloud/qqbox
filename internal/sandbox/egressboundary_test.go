package sandbox

import (
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// TestEgressBoundary checks that, once an egress allowlist is set, the internal
// ranges are a real boundary: a name resolving to a public address becomes
// connectable, but one resolving to private / cloud-metadata does not, unless
// explicitly opted in (AllowCIDRs). The host's loopback is never connectable,
// allowlist or not. With no allowlist, everything else stays open.
func TestEgressBoundary(t *testing.T) {
	lookup := func(ips ...string) func(string, string) ([]net.IP, dnsmessage.RCode) {
		return func(_, _ string) ([]net.IP, dnsmessage.RCode) {
			out := make([]net.IP, 0, len(ips))
			for _, s := range ips {
				out = append(out, net.ParseIP(s))
			}
			return out, dnsmessage.RCodeSuccess
		}
	}

	// A public resolved address is pinned and connectable.
	e := &egress{allowDomains: []string{"ok.com"}}
	e.answer(dnsQuery(t, 1, "ok.com."), lookup("93.184.216.34"))
	if !e.connectAllowed(net.ParseIP("93.184.216.34"), 443) {
		t.Error("public resolved IP should be connectable")
	}

	// A name resolving to the cloud-metadata address does NOT become reachable.
	em := &egress{allowDomains: []string{"evil.com"}}
	em.answer(dnsQuery(t, 2, "evil.com."), lookup("169.254.169.254"))
	if em.connectAllowed(net.ParseIP("169.254.169.254"), 80) {
		t.Error("metadata IP from a resolved name must be refused (SSRF/rebinding)")
	}

	// Same for a private (RFC1918) address.
	ep := &egress{allowDomains: []string{"evil.com"}}
	ep.answer(dnsQuery(t, 3, "evil.com."), lookup("10.0.0.5"))
	if ep.connectAllowed(net.ParseIP("10.0.0.5"), 80) {
		t.Error("private IP from a resolved name must be refused")
	}

	// An explicit CIDR opts a private range back in.
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	ec := &egress{allowDomains: []string{"x.com"}, allowCIDRs: []*net.IPNet{cidr}}
	if !ec.connectAllowed(net.ParseIP("10.0.0.5"), 80) {
		t.Error("an explicit AllowCIDRs should permit the private range")
	}

	// The host's loopback (and 0.0.0.0, which routes to localhost) is never a
	// target, whatever the policy: the guest's loopback is its own. Not even a
	// CIDR covering it opens it.
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")
	for _, el := range []*egress{{}, {allowDomains: []string{"x.com"}}, {allowCIDRs: []*net.IPNet{lo}}} {
		if el.connectAllowed(net.ParseIP("127.0.0.1"), 8080) || el.connectAllowed(net.IPv4zero, 8080) || el.connectAllowed(net.IPv6loopback, 8080) {
			t.Errorf("the host's loopback must be refused (%+v)", el)
		}
	}

	// No allowlist at all: everything else open.
	eo := &egress{}
	if !eo.connectAllowed(net.ParseIP("8.8.8.8"), 1) {
		t.Error("with no allowlist, every connection should be permitted")
	}
}
