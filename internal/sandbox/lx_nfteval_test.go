//go:build darwin || linux || windows

package sandbox

import "testing"

func be32b(v uint32) []byte { return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }

// expression builders in the wire format nft emits (see nft --debug=netlink).
func nftExpr(name string, data []byte) []byte {
	elem := append(rtattr(nftaExprName, cstrb(name)), rtattr(nftaExprData, data)...)
	return rtattr(nftaListElem, elem)
}
func cat(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}
func exMeta(dreg, key uint32) []byte {
	return nftExpr("meta", cat(rtattr(nftaMetaDReg, be32b(dreg)), rtattr(nftaMetaKey, be32b(key))))
}
func exPayload(dreg, base, off, l uint32) []byte {
	return nftExpr("payload", cat(rtattr(nftaPayloadDReg, be32b(dreg)), rtattr(nftaPayloadBase, be32b(base)), rtattr(nftaPayloadOffset, be32b(off)), rtattr(nftaPayloadLen, be32b(l))))
}
func exCmp(sreg uint32, op int, val []byte) []byte {
	return nftExpr("cmp", cat(rtattr(nftaCmpSReg, be32b(sreg)), rtattr(nftaCmpOp, be32b(uint32(op))), rtattr(nftaCmpData, rtattr(nftaDataValue, val))))
}
func exCt(dreg, key uint32) []byte {
	return nftExpr("ct", cat(rtattr(nftaCtDReg, be32b(dreg)), rtattr(nftaCtKey, be32b(key))))
}
func exBitwise(sreg, dreg uint32, mask, xor []byte) []byte {
	return nftExpr("bitwise", cat(rtattr(nftaBitwiseSReg, be32b(sreg)), rtattr(nftaBitwiseDReg, be32b(dreg)), rtattr(nftaBitwiseLen, be32b(uint32(len(mask)))), rtattr(nftaBitwiseMask, rtattr(nftaDataValue, mask)), rtattr(nftaBitwiseXor, rtattr(nftaDataValue, xor))))
}
func exVerdict(code int32, chain string) []byte {
	v := rtattr(nftaVerdictCode, be32b(uint32(code)))
	if chain != "" {
		v = append(v, rtattr(nftaVerdictChain, cstrb(chain))...)
	}
	return nftExpr("immediate", cat(rtattr(nftaImmediateDReg, be32b(0)), rtattr(nftaImmediateData, rtattr(nftaDataVerdict, v))))
}
func exImm(dreg uint32, val []byte) []byte {
	return nftExpr("immediate", cat(rtattr(nftaImmediateDReg, be32b(dreg)), rtattr(nftaImmediateData, rtattr(nftaDataValue, val))))
}
func exMasq() []byte { return nftExpr("masq", nil) }
func exDnat(addrReg, protoReg uint32) []byte {
	return nftExpr("nat", cat(rtattr(nftaNatType, be32b(natTypeDNAT)), rtattr(nftaNatRegAddrMin, be32b(addrReg)), rtattr(nftaNatRegProtoMin, be32b(protoReg))))
}

func rule(parts ...[]byte) *nftRule { return &nftRule{exprs: cat(parts...)} }

func exTarget(name string, info []byte) []byte {
	d := append(rtattr(nftaTargetName, cstrb(name)), rtattr(2, be32b(0))...) // name + rev 0
	if info != nil {
		d = append(d, rtattr(nftaTargetInfo, info)...)
	}
	return nftExpr("target", d)
}

// TestNftEvalTarget covers the iptables-nft (xtables-compat) target path: native
// matches followed by a MASQUERADE or DNAT target expression.
func TestNftEvalTarget(t *testing.T) {
	v4 := func(a, b, c, d byte) []byte { return []byte{a, b, c, d} }
	post := &nftChain{hooked: true, hooknum: hookPostrouting, prio: 100, policy: vcAccept, rules: []*nftRule{
		rule(exMeta(1, metaNFProto), exCmp(1, cmpEq, []byte{2}),
			exPayload(1, payloadNetwork, 12, 2), exCmp(1, cmpEq, []byte{10, 88}),
			exTarget("MASQUERADE", nil)),
	}}
	tbl := &nftTable{family: 1, chains: []*nftChain{post}}
	res := &nftResult{}
	tbl.evalChain(post, &nftPkt{nfproto: 2, saddr: v4(10, 88, 0, 2), daddr: v4(1, 1, 1, 1)}, res, 0)
	if !res.masq {
		t.Fatal("MASQUERADE target should masquerade")
	}

	info := make([]byte, 40) // struct nf_nat_range2: min_addr@4, min_proto port@36
	copy(info[4:8], v4(10, 88, 0, 2))
	info[36], info[37] = 0, 80
	pre := &nftChain{hooked: true, hooknum: hookPrerouting, prio: -100, policy: vcAccept, rules: []*nftRule{
		rule(exMeta(1, metaL4Proto), exCmp(1, cmpEq, []byte{6}),
			exPayload(1, payloadTransport, 2, 2), exCmp(1, cmpEq, []byte{0x46, 0xa0}),
			exTarget("DNAT", info)),
	}}
	tbl.chains = append(tbl.chains, pre)
	r2 := &nftResult{}
	tbl.evalChain(pre, &nftPkt{nfproto: 2, l4proto: 6, dport: 18080, daddr: v4(203, 0, 113, 5)}, r2, 0)
	if !r2.dnat || string(r2.dnatAddr) != string(v4(10, 88, 0, 2)) || r2.dnatPort != 80 {
		t.Fatalf("DNAT target wrong: %+v", r2)
	}
}

func TestNftEvalHook(t *testing.T) {
	v4 := func(a, b, c, d byte) []byte { return []byte{a, b, c, d} }
	post := &nftChain{name: "post", hooked: true, hooknum: hookPostrouting, prio: 100, policy: vcAccept, rules: []*nftRule{
		rule(exMeta(1, metaNFProto), exCmp(1, cmpEq, []byte{2}),
			exPayload(1, payloadNetwork, 12, 2), exCmp(1, cmpEq, []byte{10, 88}), exMasq()),
	}}
	s := &nftState{tables: []*nftTable{{family: 1, name: "nv", chains: []*nftChain{post}}}}
	if r := s.evalHook(hookPostrouting, &nftPkt{nfproto: 2, saddr: v4(10, 88, 0, 2), daddr: v4(1, 1, 1, 1)}); !r.masq {
		t.Fatal("postrouting hook should masquerade 10.88/16 egress")
	}
	if r := s.evalHook(hookPrerouting, &nftPkt{nfproto: 2, saddr: v4(10, 88, 0, 2)}); r.masq || r.drop || r.dnat {
		t.Fatal("a hook with no chains should do nothing")
	}
	// A v4-only (ip) table must not apply to a v6 packet.
	s2 := &nftState{tables: []*nftTable{{family: 2, name: "v4only", chains: []*nftChain{post}}}}
	if r := s2.evalHook(hookPostrouting, &nftPkt{nfproto: 10, saddr: make([]byte, 16)}); r.masq {
		t.Fatal("a v4 table must not apply to a v6 packet")
	}
}

func TestNftEval(t *testing.T) {
	v4 := func(a, b, c, d byte) []byte { return []byte{a, b, c, d} }

	// FORWARD-like filter chain, policy drop: accept 10.88/16 sources, else drop.
	fwd := &nftChain{hooked: true, policy: vcDrop, rules: []*nftRule{
		rule(exMeta(1, metaNFProto), exCmp(1, cmpEq, []byte{2}),
			exPayload(1, payloadNetwork, 12, 2), exCmp(1, cmpEq, []byte{10, 88}),
			exVerdict(vcAccept, "")),
	}}
	tbl := &nftTable{family: 1, chains: []*nftChain{fwd}}

	eval := func(c *nftChain, pkt *nftPkt) *nftResult {
		res := &nftResult{}
		tbl.evalChain(c, pkt, res, 0)
		return res
	}

	if r := eval(fwd, &nftPkt{nfproto: 2, saddr: v4(10, 88, 0, 2), daddr: v4(10, 88, 0, 3)}); r.drop {
		t.Fatal("10.88/16 source should be accepted, not dropped")
	}
	if r := eval(fwd, &nftPkt{nfproto: 2, saddr: v4(10, 99, 0, 2), daddr: v4(10, 88, 0, 3)}); !r.drop {
		t.Fatal("off-subnet source should hit the drop policy")
	}

	// ct state established,related accept.
	ctc := &nftChain{hooked: true, policy: vcDrop, rules: []*nftRule{
		rule(exCt(1, ctStateKey), exBitwise(1, 1, []byte{6, 0, 0, 0}, []byte{0, 0, 0, 0}),
			exCmp(1, cmpNeq, []byte{0, 0, 0, 0}), exVerdict(vcAccept, "")),
	}}
	tbl.chains = append(tbl.chains, ctc)
	if r := eval(ctc, &nftPkt{nfproto: 2, ctState: 2}); r.drop {
		t.Fatal("established connection should be accepted")
	}
	if r := eval(ctc, &nftPkt{nfproto: 2, ctState: 8}); !r.drop {
		t.Fatal("a new connection should not match established,related")
	}

	// POSTROUTING nat: masquerade 10.88/16.
	post := &nftChain{hooked: true, policy: vcAccept, rules: []*nftRule{
		rule(exMeta(1, metaNFProto), exCmp(1, cmpEq, []byte{2}),
			exPayload(1, payloadNetwork, 12, 2), exCmp(1, cmpEq, []byte{10, 88}), exMasq()),
	}}
	tbl.chains = append(tbl.chains, post)
	if r := eval(post, &nftPkt{nfproto: 2, saddr: v4(10, 88, 0, 2), daddr: v4(1, 1, 1, 1)}); !r.masq {
		t.Fatal("10.88/16 egress should masquerade")
	}
	if r := eval(post, &nftPkt{nfproto: 2, saddr: v4(192, 168, 0, 5), daddr: v4(1, 1, 1, 1)}); r.masq {
		t.Fatal("off-subnet source should not masquerade")
	}

	// PREROUTING dnat: tcp dport 18080 -> 10.88.0.2:80.
	pre := &nftChain{hooked: true, policy: vcAccept, rules: []*nftRule{
		rule(exMeta(1, metaL4Proto), exCmp(1, cmpEq, []byte{6}),
			exPayload(1, payloadTransport, 2, 2), exCmp(1, cmpEq, []byte{0x46, 0xa0}),
			exImm(1, v4(10, 88, 0, 2)), exImm(2, []byte{0, 80}), exDnat(1, 2)),
	}}
	tbl.chains = append(tbl.chains, pre)
	r := eval(pre, &nftPkt{nfproto: 2, l4proto: 6, dport: 18080, daddr: v4(203, 0, 113, 5)})
	if !r.dnat || string(r.dnatAddr) != string(v4(10, 88, 0, 2)) || r.dnatPort != 80 {
		t.Fatalf("dnat wrong: %+v", r)
	}
	if r := eval(pre, &nftPkt{nfproto: 2, l4proto: 6, dport: 9999}); r.dnat {
		t.Fatal("non-published port should not dnat")
	}
}
