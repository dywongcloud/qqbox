package sandbox

import (
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestEgressDomainAllowed(t *testing.T) {
	cases := []struct {
		allow []string
		name  string
		want  bool
	}{
		{nil, "anything.com", true}, // empty allowlist permits all
		{[]string{"example.com"}, "example.com", true},
		{[]string{"example.com"}, "api.example.com", true}, // subdomain
		{[]string{"example.com"}, "example.org", false},
		{[]string{"example.com"}, "notexample.com", false}, // not a subdomain
		{[]string{"example.com"}, "EXAMPLE.com", true},     // case-insensitive
		{[]string{"a.com", "b.net"}, "x.b.net", true},
	}
	for _, c := range cases {
		e := &egress{allowDomains: c.allow}
		if got := e.domainAllowed(c.name); got != c.want {
			t.Errorf("domainAllowed(%q) with %v = %v, want %v", c.name, c.allow, got, c.want)
		}
	}
}

func dnsQuery(t *testing.T, id uint16, name string) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	q, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// A disallowed domain gets REFUSED without a lookup; an allowed one is
// answered from the lookup and its address may then be dialed.
func TestEgressAnswer(t *testing.T) {
	e := &egress{allowDomains: []string{"allowed.com"}}
	lookup := func(network, name string) ([]net.IP, dnsmessage.RCode) {
		if name != "api.allowed.com" {
			t.Errorf("lookup of %q", name)
		}
		return []net.IP{net.IPv4(192, 0, 2, 7)}, dnsmessage.RCodeSuccess
	}
	var p dnsmessage.Parser
	h, err := p.Start(e.answer(dnsQuery(t, 0x1234, "blocked.example.com."), lookup))
	if err != nil || !h.Response || h.RCode != dnsmessage.RCodeRefused || h.ID != 0x1234 {
		t.Fatalf("blocked: %+v %v, want a refused echo", h, err)
	}
	h, err = p.Start(e.answer(dnsQuery(t, 0x99, "api.allowed.com."), lookup))
	if err != nil || h.RCode != dnsmessage.RCodeSuccess || h.ID != 0x99 {
		t.Fatalf("allowed: %+v %v", h, err)
	}
	p.SkipAllQuestions()
	var a dnsmessage.AResource
	if _, err = p.AnswerHeader(); err == nil {
		a, err = p.AResource()
	}
	if a.A != [4]byte{192, 0, 2, 7} {
		t.Fatalf("answer %v (%v)", a.A, err)
	}
	if !e.connectAllowed(net.IPv4(192, 0, 2, 7), 443) || e.connectAllowed(net.IPv4(192, 0, 2, 8), 443) {
		t.Fatal("the resolved address, and only it, may be dialed")
	}
}

// A resolver failure and a missing name must reach the guest as SERVFAIL and
// NXDOMAIN, not an empty NOERROR (which it would cache as a real negative).
func TestEgressDNSResultCodes(t *testing.T) {
	e := &egress{allowDomains: []string{"x.com"}}
	for _, rc := range []dnsmessage.RCode{dnsmessage.RCodeServerFailure, dnsmessage.RCodeNameError} {
		lookup := func(_, _ string) ([]net.IP, dnsmessage.RCode) { return nil, rc }
		var p dnsmessage.Parser
		h, err := p.Start(e.answer(dnsQuery(t, 7, "a.x.com."), lookup))
		if err != nil || h.RCode != rc {
			t.Errorf("RCode=%v err=%v, want %v", h.RCode, err, rc)
		}
	}
}

// DNS over a stream: length-framed queries, fed in pieces, get framed answers.
func TestEgressStream(t *testing.T) {
	e := &egress{allowDomains: []string{"allowed.test"}}
	c, s := net.Pipe()
	go e.serveStream(s)
	defer c.Close()
	q := dnsQuery(t, 0x2222, "blocked.test.")
	framed := append([]byte{byte(len(q) >> 8), byte(len(q))}, q...)
	go func() {
		c.Write(framed[:3])
		c.Write(framed[3:])
	}()
	var hdr [2]byte
	if _, err := c.Read(hdr[:]); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, int(hdr[0])<<8|int(hdr[1]))
	for n := 0; n < len(out); {
		m, err := c.Read(out[n:])
		if err != nil {
			t.Fatal(err)
		}
		n += m
	}
	var p dnsmessage.Parser
	h, err := p.Start(out)
	if err != nil || h.RCode != dnsmessage.RCodeRefused || h.ID != 0x2222 {
		t.Fatalf("got %+v %v, want a refused echo", h, err)
	}
}
