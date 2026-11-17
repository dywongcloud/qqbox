//go:build darwin || linux || windows

package sandbox

import "testing"

// nfgen builds a message body: struct nfgenmsg (family, version 0, res_id 0)
// followed by attributes.
func nfgen(family byte, attrs ...[]byte) []byte {
	b := []byte{family, 0, 0, 0}
	for _, a := range attrs {
		b = append(b, a...)
	}
	return b
}

func TestNftDispatch(t *testing.T) {
	g := &dguest{}
	s := &nftState{gen: 1}
	const inet = 1

	// GETGEN -> NEWGEN carrying the generation.
	r, e := g.nftDispatch(s, nftMsgGetGen, 0, 7, 1, nfgen(0))
	if e != 0 || len(r) != 1 {
		t.Fatalf("getgen: errno %d, %d replies", e, len(r))
	}
	gm := r[0]
	if typ := uint16(gm[4]) | uint16(gm[5])<<8; typ != uint16(nfnlSubsysNftables<<8|nftMsgNewGen) {
		t.Fatalf("getgen reply type %#x", typ)
	}
	if g := be32(parseNlAttrs(gm[nlmsgHdrLen+nfgenmsgLen:])[nftaGenID]); g != 1 {
		t.Fatalf("getgen id = %d, want 1", g)
	}

	// NEWTABLE inet "nat".
	if _, e := g.nftDispatch(s, nftMsgNewTable, 0, 1, 1, nfgen(inet, rtattr(nftaTableName, cstrb("nat")))); e != 0 {
		t.Fatalf("newtable: errno %d", e)
	}
	tbl := s.table(inet, "nat")
	if tbl == nil || tbl.handle == 0 {
		t.Fatalf("table not created: %+v", tbl)
	}

	// NEWCHAIN "post" as a base nat chain, hook postrouting(4) prio 100, policy accept(1).
	hook := rtattr(nftaChainHook, append(nftAttrBE32(nftaHookHooknum, 4), nftAttrBE32(nftaHookPriority, 100)...))
	chainBody := nfgen(inet,
		rtattr(nftaChainTable, cstrb("nat")),
		rtattr(nftaChainName, cstrb("post")),
		rtattr(nftaChainType, cstrb("nat")),
		nftAttrBE32(nftaChainPolicy, 1),
		hook)
	if _, e := g.nftDispatch(s, nftMsgNewChain, 0, 1, 1, chainBody); e != 0 {
		t.Fatalf("newchain: errno %d", e)
	}
	c := tbl.chain("post")
	if c == nil || !c.hooked || c.hooknum != 4 || c.prio != 100 || c.kind != "nat" || c.policy != vcAccept {
		t.Fatalf("chain wrong: %+v", c)
	}

	// A base chain given no policy accepts, as the kernel's does (NF_ACCEPT).
	if _, e := g.nftDispatch(s, nftMsgNewChain, 0, 1, 1, nfgen(inet,
		rtattr(nftaChainTable, cstrb("nat")),
		rtattr(nftaChainName, cstrb("pre")),
		rtattr(nftaChainType, cstrb("nat")),
		hook)); e != 0 {
		t.Fatalf("newchain without a policy: errno %d", e)
	}
	if c := tbl.chain("pre"); c == nil || c.policy != vcAccept {
		t.Fatalf("a chain without a policy: %+v", c)
	}

	// NEWRULE in "post" with some opaque expressions.
	exprs := []byte{0xde, 0xad, 0xbe, 0xef}
	if _, e := g.nftDispatch(s, nftMsgNewRule, 0, 1, 1, nfgen(inet,
		rtattr(nftaRuleTable, cstrb("nat")),
		rtattr(nftaRuleChain, cstrb("post")),
		rtattr(nftaRuleExpressions, exprs))); e != 0 {
		t.Fatalf("newrule: errno %d", e)
	}
	if len(c.rules) != 1 || string(c.rules[0].exprs) != string(exprs) || c.rules[0].handle == 0 {
		t.Fatalf("rule wrong: %+v", c.rules)
	}

	// GETTABLE dump returns the created table (what `nft list` reads).
	gr, e := g.nftDispatch(s, nftMsgGetTable, nlmFDump, 1, 1, nfgen(0))
	if e != 0 {
		t.Fatalf("gettable dump: errno %d", e)
	}
	foundTbl := false
	for _, m := range gr {
		if typ := uint16(m[4]) | uint16(m[5])<<8; typ == uint16(nfnlSubsysNftables<<8|nftMsgNewTable) {
			if cstr(parseNlAttrs(m[nlmsgHdrLen+nfgenmsgLen:])[nftaTableName]) == "nat" {
				foundTbl = true
			}
		}
	}
	if !foundTbl {
		t.Fatal("gettable dump missing the created table")
	}
	// GETRULE dump returns the rule with its expressions replayed.
	rr, e := g.nftDispatch(s, nftMsgGetRule, nlmFDump, 1, 1, nfgen(inet))
	if e != 0 || len(rr) < 2 {
		t.Fatalf("getrule dump: errno %d, %d msgs", e, len(rr))
	}
	if ex := parseNlAttrs(rr[0][nlmsgHdrLen+nfgenmsgLen:])[nftaRuleExpressions]; string(ex) != string(exprs) {
		t.Fatalf("getrule expressions not replayed: %x", ex)
	}

	// A chain in a missing table is ENOENT.
	if _, e := g.nftDispatch(s, nftMsgNewChain, 0, 1, 1, nfgen(inet, rtattr(nftaChainTable, cstrb("absent")), rtattr(nftaChainName, cstrb("x")))); e != lxENOENT {
		t.Fatalf("newchain missing table: want ENOENT, got %d", e)
	}

	// DELTABLE removes it.
	if _, e := g.nftDispatch(s, nftMsgDelTable, 0, 1, 1, nfgen(inet, rtattr(nftaTableName, cstrb("nat")))); e != 0 {
		t.Fatalf("deltable: errno %d", e)
	}
	if s.table(inet, "nat") != nil {
		t.Fatal("table survived deltable")
	}

	// A GET before serialization exists reports ENOENT (so a presence check creates).
	if _, e := g.nftDispatch(s, nftMsgGetTable, 0, 1, 1, nfgen(inet, rtattr(nftaTableName, cstrb("whatever")))); e != lxENOENT {
		t.Fatalf("gettable: want ENOENT, got %d", e)
	}
}

// cstrb is a NUL-terminated string attribute value.
func cstrb(s string) []byte { return append([]byte(s), 0) }
