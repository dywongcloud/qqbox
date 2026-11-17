//go:build darwin || linux || windows

package sandbox

import (
	"net"
	"sort"
)

// Netfilter hooks (NF_INET_*), the point in a connection's path at which base
// chains of a given priority run.
const (
	hookPrerouting  = 0
	hookLocalIn     = 1
	hookForward     = 2
	hookLocalOut    = 3
	hookPostrouting = 4
)

// nftables expression evaluation: the verdict and NAT half of the firewall. A
// base chain's rules are a sequence of expressions that load packet fields into
// registers, compare and mask them, and -- when every match in a rule passes --
// apply statements (a verdict, masquerade, dnat). laboon services connections,
// not frames, so it evaluates a chain against a synthetic packet built from a
// connection's 5-tuple at the connect/accept/egress/publish points, rather than
// on a per-frame hook path. Expression encodings were taken from `nft --debug`.
//
// The evaluator is generic: it executes whatever expressions a rule holds, from
// any table or chain, recognizing nothing by name.

// expression attribute types (within NFTA_EXPR_DATA), per expression.
const (
	nftaListElem = 1
	nftaExprName = 1
	nftaExprData = 2
	nftaDataValue   = 1
	nftaDataVerdict = 2
	nftaVerdictCode  = 1
	nftaVerdictChain = 2

	nftaPayloadDReg   = 1
	nftaPayloadBase   = 2
	nftaPayloadOffset = 3
	nftaPayloadLen    = 4
	payloadNetwork    = 1 // NFT_PAYLOAD_NETWORK_HEADER
	payloadTransport  = 2 // NFT_PAYLOAD_TRANSPORT_HEADER

	nftaCmpSReg = 1
	nftaCmpOp   = 2
	nftaCmpData = 3
	cmpEq  = 0
	cmpNeq = 1
	cmpLt  = 2
	cmpLte = 3
	cmpGt  = 4
	cmpGte = 5

	nftaBitwiseSReg = 1
	nftaBitwiseDReg = 2
	nftaBitwiseLen  = 3
	nftaBitwiseMask = 4
	nftaBitwiseXor  = 5

	nftaMetaDReg = 1
	nftaMetaKey  = 2
	metaMark    = 3
	metaNFProto = 15
	metaL4Proto = 16

	nftaCtDReg = 1
	nftaCtKey  = 2
	ctStateKey = 0

	nftaFibDReg   = 1
	nftaFibResult = 2
	fibResultAddrType = 3

	nftaLookupSet   = 1
	nftaLookupSReg  = 2
	nftaLookupFlags = 4
	lookupFInv      = 1

	nftaImmediateDReg = 1
	nftaImmediateData = 2

	nftaNatType       = 1
	nftaNatRegAddrMin = 3
	nftaNatRegProtoMin = 5
	natTypeSNAT = 0
	natTypeDNAT = 1

	// xtables-compat target/match expressions (how iptables-nft carries an
	// extension such as MASQUERADE or a conntrack match).
	nftaTargetName = 1
	nftaTargetInfo = 3
	nftaMatchName  = 1
	nftaMatchInfo  = 3

	// verdict codes (NFTA_VERDICT_CODE, read as signed)
	vcDrop     = 0
	vcAccept   = 1
	vcContinue = -1
	vcJump     = -3
	vcGoto     = -4
	vcReturn   = -5
)

// nftPkt is the synthetic packet a connection presents to the evaluator.
type nftPkt struct {
	nfproto uint8  // 2 = IPv4, 10 = IPv6
	l4proto uint8  // 6 = TCP, 17 = UDP, 1 = ICMP
	saddr   []byte // 4 or 16 bytes, network order
	daddr   []byte
	sport   uint16
	dport   uint16
	mark    uint32
	ctState uint32 // bitmask: invalid 1, established 2, related 4, new 8
	// fibType reports the RTN_* type of an address (2 = local), from the model.
	fibType func(ip []byte) uint32
	// setHas reports membership in a named set of the same table.
	setHas func(set string, val []byte) bool
}

// nftResult is the outcome of evaluating a base chain for a packet.
type nftResult struct {
	drop      bool
	masq      bool
	dnat      bool
	dnatAddr  []byte
	dnatPort  uint16
	mark      uint32 // mark set during evaluation
}

// evalHook evaluates every base chain hooked at hook, across all tables of the
// namespace that apply to the packet's family, in priority order (lower first),
// and returns the combined result. A drop from any chain stops evaluation; NAT
// and mark effects accumulate. This is the entry the connection path calls at
// each stage (prerouting dnat, forward filter, postrouting snat). Caller holds
// n.mu.
func (s *nftState) evalHook(hook int32, pkt *nftPkt) *nftResult {
	type based struct {
		t *nftTable
		c *nftChain
	}
	var chains []based
	for _, t := range s.tables {
		applies := t.family == 1 || (t.family == 2 && pkt.nfproto == 2) || (t.family == 10 && pkt.nfproto == 10)
		if !applies {
			continue
		}
		for _, c := range t.chains {
			if c.hooked && c.hooknum == hook {
				chains = append(chains, based{t, c})
			}
		}
	}
	sort.SliceStable(chains, func(i, j int) bool { return chains[i].c.prio < chains[j].c.prio })
	res := &nftResult{}
	for _, b := range chains {
		if b.t.evalChain(b.c, pkt, res, 0) == vcDrop {
			break
		}
	}
	return res
}

// evalChain evaluates chain c for pkt, following jumps up to a depth bound, and
// returns the verdict code and accumulated NAT/mark effects. A base chain's
// policy applies when no rule reaches a terminal verdict.
func (t *nftTable) evalChain(c *nftChain, pkt *nftPkt, res *nftResult, depth int) int {
	if depth > 16 {
		return vcContinue
	}
	for _, r := range c.rules {
		regs := map[uint32][]byte{}
		verdict := vcContinue
		var jumpTo string
		matched := true
		for _, e := range parseNlAttrList(r.exprs) {
			if e.typ != nftaListElem {
				continue
			}
			ea := parseNlAttrs(e.val)
			name := cstr(ea[nftaExprName])
			d := ea[nftaExprData]
			if !evalExpr(name, d, regs, pkt, res, &verdict, &jumpTo) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		switch verdict {
		case vcAccept:
			return vcAccept
		case vcDrop:
			res.drop = true
			return vcDrop
		case vcReturn:
			return vcReturn
		case vcJump, vcGoto:
			if tc := t.chain(jumpTo); tc != nil {
				v := t.evalChain(tc, pkt, res, depth+1)
				if v == vcAccept || v == vcDrop {
					return v
				}
				if verdict == vcGoto {
					return v
				}
				// jump: a return/continue falls through to the next rule here
			}
		}
	}
	if c.hooked { // base chain: apply its policy when nothing else decided
		if c.policy == vcDrop {
			res.drop = true
			return vcDrop
		}
		return vcAccept
	}
	return vcContinue
}

// evalExpr executes one expression. It returns false when a match expression
// (cmp, lookup) fails, which stops the rule. Statements (verdict, masq, nat,
// counter, loads) return true.
func evalExpr(name string, d []byte, regs map[uint32][]byte, pkt *nftPkt, res *nftResult, verdict *int, jumpTo *string) bool {
	a := parseNlAttrs(d)
	switch name {
	case "payload":
		dreg := be32(a[nftaPayloadDReg])
		base := be32(a[nftaPayloadBase])
		off := int(be32(a[nftaPayloadOffset]))
		l := int(be32(a[nftaPayloadLen]))
		regs[dreg] = loadPayload(pkt, int(base), off, l)
		return true
	case "meta":
		dreg := be32(a[nftaMetaDReg])
		regs[dreg] = loadMeta(pkt, be32(a[nftaMetaKey]))
		return true
	case "ct":
		dreg := be32(a[nftaCtDReg])
		if be32(a[nftaCtKey]) == ctStateKey {
			regs[dreg] = le32b(pkt.ctState)
		}
		return true
	case "fib":
		dreg := be32(a[nftaFibDReg])
		if be32(a[nftaFibResult]) == fibResultAddrType && pkt.fibType != nil {
			regs[dreg] = le32b(pkt.fibType(pkt.daddr))
		} else {
			regs[dreg] = le32b(0)
		}
		return true
	case "bitwise":
		sreg := be32(a[nftaBitwiseSReg])
		dreg := be32(a[nftaBitwiseDReg])
		mask := dataValue(a[nftaBitwiseMask])
		xor := dataValue(a[nftaBitwiseXor])
		regs[dreg] = andXor(regs[sreg], mask, xor)
		return true
	case "cmp":
		sreg := be32(a[nftaCmpSReg])
		op := int(be32(a[nftaCmpOp]))
		return cmpReg(regs[sreg], op, dataValue(a[nftaCmpData]))
	case "lookup":
		sreg := be32(a[nftaLookupSReg])
		set := cstr(a[nftaLookupSet])
		in := pkt.setHas != nil && pkt.setHas(set, regs[sreg])
		if be32(a[nftaLookupFlags])&lookupFInv != 0 {
			return !in
		}
		return in
	case "immediate":
		dreg := be32(a[nftaImmediateDReg])
		data := a[nftaImmediateData]
		if v := parseNlAttrs(data)[nftaDataVerdict]; v != nil { // a verdict
			vc := parseNlAttrs(v)
			*verdict = int(int32(be32(vc[nftaVerdictCode])))
			*jumpTo = cstr(vc[nftaVerdictChain])
		} else {
			regs[dreg] = dataValue(data)
		}
		return true
	case "masq":
		res.masq = true
		return true
	case "nat":
		if be32(a[nftaNatType]) == natTypeDNAT {
			res.dnat = true
			res.dnatAddr = append([]byte(nil), regs[be32(a[nftaNatRegAddrMin])]...)
			if p := regs[be32(a[nftaNatRegProtoMin])]; len(p) >= 2 {
				res.dnatPort = uint16(p[0])<<8 | uint16(p[1]) // network order
			}
		} else {
			res.masq = true // snat to a fixed address behaves like masquerade here
		}
		return true
	case "target":
		// An iptables (xtables-compat) target. The standard NAT targets map onto
		// the same verdict/NAT effects as the native nft statements.
		switch cstr(a[nftaTargetName]) {
		case "MASQUERADE", "SNAT":
			res.masq = true
		case "DNAT", "REDIRECT":
			res.dnat = true
			if info := a[nftaTargetInfo]; len(info) >= 38 { // struct nf_nat_range2
				res.dnatAddr = append([]byte(nil), info[4:8]...) // min_addr (IPv4)
				res.dnatPort = uint16(info[36])<<8 | uint16(info[37])
			}
		case "ACCEPT":
			*verdict = vcAccept
		case "DROP", "REJECT":
			*verdict = vcDrop
		case "RETURN":
			*verdict = vcReturn
		}
		return true
	case "counter", "log", "notrack":
		return true
	}
	return true // an unknown expression does not fail the match
}

// loadPayload reads l bytes at offset off of the network or transport header.
func loadPayload(pkt *nftPkt, base, off, l int) []byte {
	var hdr []byte
	switch base {
	case payloadNetwork:
		hdr = ipHeader(pkt)
	case payloadTransport:
		hdr = l4Header(pkt)
	}
	out := make([]byte, l)
	if off+l <= len(hdr) {
		copy(out, hdr[off:off+l])
	}
	return out
}

// ipHeader synthesizes the IP header bytes the rules read (addresses at the
// kernel's offsets): IPv4 saddr @12/daddr @16, IPv6 saddr @8/daddr @24.
func ipHeader(pkt *nftPkt) []byte {
	if pkt.nfproto == 10 {
		h := make([]byte, 40)
		copy(h[8:24], pkt.saddr)
		copy(h[24:40], pkt.daddr)
		return h
	}
	h := make([]byte, 20)
	copy(h[12:16], pkt.saddr)
	copy(h[16:20], pkt.daddr)
	return h
}

// l4Header synthesizes a transport header: sport @0, dport @2 (network order).
func l4Header(pkt *nftPkt) []byte {
	h := make([]byte, 8)
	h[0], h[1] = byte(pkt.sport>>8), byte(pkt.sport)
	h[2], h[3] = byte(pkt.dport>>8), byte(pkt.dport)
	return h
}

func loadMeta(pkt *nftPkt, key uint32) []byte {
	switch key {
	case metaNFProto:
		return []byte{pkt.nfproto}
	case metaL4Proto:
		return []byte{pkt.l4proto}
	case metaMark:
		return le32b(pkt.mark)
	}
	return nil
}

// le32b renders v as the 4 host-order bytes a register holds for a scalar.
func le32b(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

// dataValue extracts NFTA_DATA_VALUE bytes from a data attribute.
func dataValue(d []byte) []byte {
	if d == nil {
		return nil
	}
	return parseNlAttrs(d)[nftaDataValue]
}

// andXor computes (src & mask) ^ xor over the mask length.
func andXor(src, mask, xor []byte) []byte {
	out := make([]byte, len(mask))
	for i := range mask {
		var s, x byte
		if i < len(src) {
			s = src[i]
		}
		if i < len(xor) {
			x = xor[i]
		}
		out[i] = (s & mask[i]) ^ x
	}
	return out
}

// cmpReg compares reg to val under op over val's length.
func cmpReg(reg []byte, op int, val []byte) bool {
	n := len(val)
	r := make([]byte, n)
	copy(r, reg)
	c := bytesCompare(r, val)
	switch op {
	case cmpEq:
		return c == 0
	case cmpNeq:
		return c != 0
	case cmpLt:
		return c < 0
	case cmpLte:
		return c <= 0
	case cmpGt:
		return c > 0
	case cmpGte:
		return c >= 0
	}
	return false
}

func bytesCompare(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// parseNlAttrList walks an attribute stream preserving order and duplicate types
// (a rule's expression list is repeated NFTA_LIST_ELEM attributes).
func parseNlAttrList(b []byte) []nlAttr {
	var out []nlAttr
	for off := 0; off+4 <= len(b); {
		l := int(rd16(b[off:]))
		typ := rd16(b[off+2:]) & 0x3fff
		if l < 4 || off+l > len(b) {
			break
		}
		out = append(out, nlAttr{typ: typ, val: b[off+4 : off+l]})
		off += (l + 3) &^ 3
	}
	return out
}

type nlAttr struct {
	typ uint16
	val []byte
}

// beIP packs a 4-byte big-endian IPv4 address into the host-order uint32 the
// vnet keys and the firewall model use.
func beIP(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// ip4 helpers used by callers building a packet.
func ipBytes(ip net.IP) []byte {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip.To16()
}

// ruleDNAT statically extracts a published port from one rule: the TCP
// destination port it matches and the IPv4 address:port it DNATs to, so the
// supervisor can realize a container runtime's -p mappings from the firewall the
// runtime installs rather than from recognizing the runtime. ok is false unless
// the rule both matches a transport destination port and DNATs to an address.
//
// Both nftables frontends encode the port match the same way -- a transport
// payload load into a register, then a cmp-equal against it -- and differ only
// in the target: a native `nat` statement whose address/port come from
// registers an `immediate` loaded, or an xtables `target` "DNAT" carrying an
// nf_nat_range2 (addr at offset 4, port at 36). This mirrors the subset of
// evalExpr the extraction needs, without a packet.
func ruleDNAT(r *nftRule) (hostPort uint16, dstAddr uint32, dstPort uint16, ok bool) {
	regs := map[uint32][]byte{} // immediate-loaded register values
	dportReg, l4Reg := int64(-1), int64(-1)
	proto := uint8(6) // tcp unless a meta l4proto match says otherwise
	var isDNAT bool
	for _, e := range parseNlAttrList(r.exprs) {
		if e.typ != nftaListElem {
			continue
		}
		ea := parseNlAttrs(e.val)
		a := parseNlAttrs(ea[nftaExprData])
		switch cstr(ea[nftaExprName]) {
		case "meta":
			if be32(a[nftaMetaKey]) == metaL4Proto {
				l4Reg = int64(be32(a[nftaMetaDReg]))
			}
		case "payload":
			if be32(a[nftaPayloadBase]) == payloadTransport && be32(a[nftaPayloadOffset]) == 2 && be32(a[nftaPayloadLen]) == 2 {
				dportReg = int64(be32(a[nftaPayloadDReg]))
			}
		case "immediate":
			if parseNlAttrs(a[nftaImmediateData])[nftaDataVerdict] == nil { // a register load, not a verdict
				regs[be32(a[nftaImmediateDReg])] = dataValue(a[nftaImmediateData])
			}
		case "cmp":
			if int(be32(a[nftaCmpOp])) != cmpEq {
				break
			}
			sreg, val := int64(be32(a[nftaCmpSReg])), dataValue(a[nftaCmpData])
			if sreg == dportReg && len(val) >= 2 {
				hostPort = uint16(val[0])<<8 | uint16(val[1])
			} else if sreg == l4Reg && len(val) >= 1 {
				proto = val[0]
			}
		case "nat":
			if be32(a[nftaNatType]) == natTypeDNAT {
				isDNAT = true
				if ad := regs[be32(a[nftaNatRegAddrMin])]; len(ad) >= 4 {
					dstAddr = beIP(ad)
				}
				if p := regs[be32(a[nftaNatRegProtoMin])]; len(p) >= 2 {
					dstPort = uint16(p[0])<<8 | uint16(p[1])
				}
			}
		case "target":
			switch cstr(a[nftaTargetName]) {
			case "DNAT", "REDIRECT":
				isDNAT = true
				if info := a[nftaTargetInfo]; len(info) >= 38 { // struct nf_nat_range2
					dstAddr = beIP(info[4:8])
					dstPort = uint16(info[36])<<8 | uint16(info[37])
				}
			}
		}
	}
	if !isDNAT || hostPort == 0 || proto != 6 {
		return 0, 0, 0, false
	}
	if dstPort == 0 { // DNAT that rewrites only the address keeps the port
		dstPort = hostPort
	}
	if dstAddr == 0 { // REDIRECT (no address) targets the host itself
		return 0, 0, 0, false
	}
	return hostPort, dstAddr, dstPort, true
}
