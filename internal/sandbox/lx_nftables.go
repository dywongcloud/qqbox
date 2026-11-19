//go:build darwin || linux || windows

package sandbox

// NETLINK_NETFILTER (the nftables subsystem): the control plane of qqbox's
// firewall, the netfilter twin of the routing-netlink model in lx_netmodel.go.
// The unmodified `nft` binary (run by a container runtime, by docker's
// iptables-nft, or by hand) opens an AF_NETLINK/NETLINK_NETFILTER socket and
// drives nftables through it; qqbox models the ruleset the guest builds -- its
// tables, chains, rules and sets, per network namespace -- so `nft -f` and
// `nft list` work. Rule expressions are kept opaque here; evaluating them on a
// connection's 5-tuple (filter verdicts and NAT) is the next step.
//
// Wire format (confirmed from a real `nft` trace): each message is an nlmsghdr
// whose type is (subsystem << 8) | message; the nftables subsystem is 10. A
// change is framed as a transaction -- a BATCH_BEGIN message, the NEW*/DEL*
// messages, then BATCH_END -- and each message body is a struct nfgenmsg
// (family, version, big-endian res_id) followed by netlink attributes.

const (
	netlinkNetfilter = 12 // socket protocol

	nfnlSubsysNftables  = 10
	nfnlSubsysNftCompat = 11
	nfnlMsgBatchBegin   = 16 // subsystem NONE
	nfnlMsgBatchEnd     = 17

	// NFT_COMPAT (subsystem 11): iptables-nft queries an xtables extension's
	// supported revision here before building a rule that carries it.
	nfnlMsgCompatGet = 0
	nfnlMsgCompatNew = 1
	nftaCompatName   = 1
	nftaCompatRev    = 2
	nftaCompatType   = 3

	// nftables message types (low byte of nlmsg_type).
	nftMsgNewTable   = 0
	nftMsgGetTable   = 1
	nftMsgDelTable   = 2
	nftMsgNewChain   = 3
	nftMsgGetChain   = 4
	nftMsgDelChain   = 5
	nftMsgNewRule    = 6
	nftMsgGetRule    = 7
	nftMsgDelRule    = 8
	nftMsgNewSet     = 9
	nftMsgGetSet     = 10
	nftMsgDelSet     = 11
	nftMsgNewSetElem = 12
	nftMsgGetSetElem = 13
	nftMsgDelSetElem = 14
	nftMsgNewGen     = 15
	nftMsgGetGen     = 16

	// struct nfgenmsg
	nfgenmsgLen = 4

	nlaFNested = 0x8000 // NLA_F_NESTED, set on a nested attribute

	// table / chain / rule / set / gen attributes
	nftaTableName       = 1
	nftaTableFlags      = 2
	nftaTableUse        = 3
	nftaTableHandle     = 4
	nftaTablePad        = 5
	nftaChainTable      = 1
	nftaChainHandle     = 2
	nftaChainName       = 3
	nftaChainHook       = 4
	nftaChainPolicy     = 5
	nftaChainUse        = 6
	nftaChainType       = 7
	nftaChainPad        = 9
	nftaHookHooknum     = 1
	nftaHookPriority    = 2
	nftaRuleTable       = 1
	nftaRuleChain       = 2
	nftaRuleHandle      = 3
	nftaRuleExpressions = 4
	nftaRulePad         = 9
	nftaSetTable        = 1
	nftaSetName         = 2
	nftaSetHandle       = 4
	nftaSetElemList     = 3 // NFTA_SET_ELEM_LIST_* (table/set/elements)
	nftaGenID           = 1
)

// A GET dump (what `nft list` reads) is rebuilt from these fields in the kernel's
// canonical attribute form (see the *Msg methods below), not by replaying what the
// guest sent, so iptables' strict libnftnl parser accepts it too.

// nftRule is one rule; its expressions are kept as the raw NFTA_RULE_EXPRESSIONS
// bytes until the evaluator parses them.
type nftRule struct {
	handle uint64
	exprs  []byte
}

// nftChain is a chain in a table. A base chain has a hook (hooked=true) with a
// hook number, priority and policy; a regular chain is only a jump target.
type nftChain struct {
	name    string
	handle  uint64
	hooked  bool
	hooknum int32
	prio    int32
	policy  uint32
	kind    string // NFTA_CHAIN_TYPE: "filter", "nat", "route"
	rules   []*nftRule
}

// nftSet is a named set; its elements are kept opaque for now.
type nftSet struct {
	name   string
	handle uint64
	elems  [][]byte
}

// nftTable is a table, identified by (family, name).
type nftTable struct {
	family uint8
	name   string
	handle uint64
	chains []*nftChain
	sets   []*nftSet
}

// nftState is one network namespace's nftables ruleset.
type nftState struct {
	tables  []*nftTable
	gen     uint32
	nextHdl uint64
}

func (s *nftState) handle() uint64 { s.nextHdl++; return s.nextHdl }

func (s *nftState) table(family uint8, name string) *nftTable {
	for _, t := range s.tables {
		if t.family == family && t.name == name {
			return t
		}
	}
	return nil
}

func (t *nftTable) chain(name string) *nftChain {
	for _, c := range t.chains {
		if c.name == name {
			return c
		}
	}
	return nil
}

func (t *nftTable) set(name string) *nftSet {
	for _, st := range t.sets {
		if st.name == name {
			return st
		}
	}
	return nil
}

// nftModel returns a namespace's nftables state, creating it on first use.
// Caller holds n.mu.
func (n *dnet) nftModel(netns int32) *nftState {
	if n.nftns == nil {
		n.nftns = map[int32]*nftState{}
	}
	s := n.nftns[netns]
	if s == nil {
		s = &nftState{gen: 1}
		n.nftns[netns] = s
	}
	return s
}

// nftReplies answers the nftables netlink requests in req for the socket's
// namespace: the reply datagrams, in order. It applies NEW*/DEL* within a batch
// to the model, answers GETGEN, and acks messages that ask for it.
func (g *dguest) nftReplies(req []byte, netns int32) []nlOut {
	pid := uint32(g.tgid)
	n := g.netState()
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.nftModel(netns)

	var out []nlOut
	committed := false
	for off := 0; off+nlmsgHdrLen <= len(req); {
		l := int(le32(req[off:]))
		if l < nlmsgHdrLen || off+l > len(req) {
			break
		}
		typ := uint16(req[off+4]) | uint16(req[off+5])<<8
		flags := uint16(req[off+6]) | uint16(req[off+7])<<8
		seq := le32(req[off+8:])
		rq := req[off : off+l] // the request message, which an error echoes
		body := req[off+nlmsgHdrLen : off+l]
		subsys := typ >> 8
		msg := typ & 0xff

		switch {
		case subsys == 0 && (msg == nfnlMsgBatchBegin):
			// transaction start; nothing to do until the messages arrive
		case subsys == 0 && (msg == nfnlMsgBatchEnd):
			s.gen++ // a committed transaction bumps the ruleset generation
			committed = true
		case subsys == nfnlSubsysNftables:
			if r, e := g.nftDispatch(s, msg, flags, seq, pid, body); e != 0 {
				out = append(out, nlOut{dgram: nlErr(seq, pid, e, rq)})
			} else {
				for _, dg := range r { // stamp the ruleset generation into nfgenmsg.res_id, as the kernel does
					if len(dg) >= 20 && dg[5] == nfnlSubsysNftables {
						dg[18], dg[19] = byte(s.gen>>8), byte(s.gen)
					}
				}
				if k := len(r); k > 0 && isDone(r[k-1]) {
					out = append(out, nlOut{dump: r, req: append([]byte(nil), rq...)}) // never acked, as any dump
				} else {
					for _, m := range r {
						out = append(out, nlOut{dgram: m})
					}
					if flags&nlmFAck != 0 {
						out = append(out, nlOut{dgram: nlErr(seq, pid, 0, rq)})
					}
				}
			}
		case subsys == nfnlSubsysNftCompat && msg == nfnlMsgCompatGet:
			// Confirm the xtables extension's revision so iptables-nft proceeds;
			// the rule it then builds carries the extension as a target/match
			// expression, evaluated in lx_nfteval.go.
			out = append(out, nlOut{dgram: nftCompatReply(seq, pid, body)})
		default:
			out = append(out, nlOut{dgram: nlErr(seq, pid, lxEOPNOTSUPP, rq)})
		}
		off += (l + 3) &^ 3
	}
	if committed {
		// Realize any -p publications the firewall now describes (host listeners
		// relaying into the virtual network); a no-op where none changed.
		n.reconcilePublish(netns)
	}
	return out
}

// nftDispatch applies or answers one nftables message. body is the message after
// the netlink header (struct nfgenmsg then attributes). Caller holds n.mu.
func (g *dguest) nftDispatch(s *nftState, msg, flags uint16, seq, pid uint32, body []byte) ([][]byte, lxErrno) {
	if len(body) < nfgenmsgLen {
		return nil, lxEINVAL
	}
	family := body[0]
	at := parseNlAttrs(body[nfgenmsgLen:])

	switch msg {
	case nftMsgGetGen:
		return [][]byte{nftGenMsg(s.gen, seq, pid)}, 0
	case nftMsgNewTable:
		name := cstr(at[nftaTableName])
		if name == "" {
			return nil, lxEINVAL
		}
		t := s.table(family, name)
		if t == nil {
			t = &nftTable{family: family, name: name, handle: s.handle()}
			s.tables = append(s.tables, t)
		}
		return nil, 0
	case nftMsgDelTable:
		name := cstr(at[nftaTableName])
		for i, t := range s.tables {
			if t.family == family && (name == "" || t.name == name) {
				s.tables = append(s.tables[:i], s.tables[i+1:]...)
				if name != "" {
					break
				}
			}
		}
		return nil, 0
	case nftMsgNewChain:
		t := s.table(family, cstr(at[nftaChainTable]))
		if t == nil {
			return nil, lxENOENT
		}
		name := cstr(at[nftaChainName])
		c := t.chain(name)
		if c == nil {
			c = &nftChain{name: name, handle: s.handle(), policy: vcAccept} // the kernel's default, NF_ACCEPT
			t.chains = append(t.chains, c)
		}
		if k := at[nftaChainType]; k != nil {
			c.kind = cstr(k)
		}
		if p := at[nftaChainPolicy]; len(p) >= 4 {
			c.policy = be32(p)
		}
		if h := at[nftaChainHook]; h != nil { // base chain
			hk := parseNlAttrs(h)
			c.hooked = true
			if v := hk[nftaHookHooknum]; len(v) >= 4 {
				c.hooknum = int32(be32(v))
			}
			if v := hk[nftaHookPriority]; len(v) >= 4 {
				c.prio = int32(be32(v))
			}
		}
		return nil, 0
	case nftMsgDelChain:
		t := s.table(family, cstr(at[nftaChainTable]))
		if t == nil {
			return nil, lxENOENT
		}
		name := cstr(at[nftaChainName])
		for i, c := range t.chains {
			if c.name == name {
				t.chains = append(t.chains[:i], t.chains[i+1:]...)
				break
			}
		}
		return nil, 0
	case nftMsgNewRule:
		t := s.table(family, cstr(at[nftaRuleTable]))
		if t == nil {
			return nil, lxENOENT
		}
		c := t.chain(cstr(at[nftaRuleChain]))
		if c == nil {
			return nil, lxENOENT
		}
		c.rules = append(c.rules, &nftRule{handle: s.handle(), exprs: append([]byte(nil), at[nftaRuleExpressions]...)})
		return nil, 0
	case nftMsgDelRule:
		t := s.table(family, cstr(at[nftaRuleTable]))
		if t == nil {
			return nil, lxENOENT
		}
		c := t.chain(cstr(at[nftaRuleChain]))
		if c == nil { // deleting all rules of a table/chain
			return nil, 0
		}
		if h := at[nftaRuleHandle]; len(h) >= 8 {
			hdl := be64(h)
			for i, r := range c.rules {
				if r.handle == hdl {
					c.rules = append(c.rules[:i], c.rules[i+1:]...)
					break
				}
			}
		} else {
			c.rules = nil // no handle: flush the chain's rules
		}
		return nil, 0
	case nftMsgNewSet:
		t := s.table(family, cstr(at[nftaSetTable]))
		if t == nil {
			return nil, lxENOENT
		}
		name := cstr(at[nftaSetName])
		st := t.set(name)
		if st == nil {
			st = &nftSet{name: name, handle: s.handle()}
			t.sets = append(t.sets, st)
		}
		return nil, 0
	case nftMsgDelSet:
		t := s.table(family, cstr(at[nftaSetTable]))
		if t == nil {
			return nil, lxENOENT
		}
		name := cstr(at[nftaSetName])
		for i, st := range t.sets {
			if st.name == name {
				t.sets = append(t.sets[:i], t.sets[i+1:]...)
				break
			}
		}
		return nil, 0
	case nftMsgNewSetElem:
		// Elements carry a nested list under NFTA_SET_ELEM_LIST_ELEMENTS; kept
		// opaque until the evaluator needs set membership.
		t := s.table(family, cstr(at[nftaSetTable]))
		if t == nil {
			return nil, lxENOENT
		}
		if st := t.set(cstr(at[nftaSetName])); st != nil {
			if e := at[nftaSetElemList]; e != nil {
				st.elems = append(st.elems, append([]byte(nil), e...))
			}
		}
		return nil, 0
	case nftMsgDelSetElem:
		return nil, 0 // flush handled coarsely for now
	case nftMsgGetTable:
		if flags&nlmFDump == 0 { // single lookup (a presence check)
			t := s.table(family, cstr(at[nftaTableName]))
			if t == nil {
				return nil, lxENOENT
			}
			return [][]byte{t.tableMsg(0, seq, pid)}, 0
		}
		name := cstr(at[nftaTableName])
		var msgs [][]byte
		for _, t := range s.tables {
			if (family != 0 && t.family != family) || (name != "" && t.name != name) {
				continue
			}
			msgs = append(msgs, t.tableMsg(nlmFMulti, seq, pid))
		}
		return append(msgs, nftDone(seq, pid)), 0
	case nftMsgGetChain:
		tname, cname := cstr(at[nftaChainTable]), cstr(at[nftaChainName])
		var msgs [][]byte
		for _, t := range s.tables {
			if (family != 0 && t.family != family) || (tname != "" && t.name != tname) {
				continue
			}
			for _, c := range t.chains {
				if cname != "" && c.name != cname {
					continue
				}
				msgs = append(msgs, t.chainMsg(c, nlmFMulti, seq, pid))
			}
		}
		return append(msgs, nftDone(seq, pid)), 0
	case nftMsgGetRule:
		tname, cname := cstr(at[nftaRuleTable]), cstr(at[nftaRuleChain])
		var msgs [][]byte
		for _, t := range s.tables {
			if (family != 0 && t.family != family) || (tname != "" && t.name != tname) {
				continue
			}
			for _, c := range t.chains {
				if cname != "" && c.name != cname {
					continue
				}
				for _, r := range c.rules {
					msgs = append(msgs, t.ruleMsg(c, r, nlmFMulti, seq, pid))
				}
			}
		}
		return append(msgs, nftDone(seq, pid)), 0
	case nftMsgGetSet:
		tname := cstr(at[nftaSetTable])
		var msgs [][]byte
		for _, t := range s.tables {
			if (family != 0 && t.family != family) || (tname != "" && t.name != tname) {
				continue
			}
			for _, st := range t.sets {
				msgs = append(msgs, t.setMsg(st, nlmFMulti, seq, pid))
			}
		}
		return append(msgs, nftDone(seq, pid)), 0
	case nftMsgGetSetElem:
		// Set elements are kept opaque; report an empty, terminated dump so
		// `nft list` does not error on a set that has no (visible) elements.
		return [][]byte{nftDone(seq, pid)}, 0
	}
	return nil, lxEOPNOTSUPP
}

// The GET dumps below build each object in the canonical attribute form the
// kernel sends -- from the model's fields, not by replaying the guest's create
// attributes -- because libnftnl (iptables in nft mode) parses dumps strictly,
// unlike nft's own libnftables.

func cstrz(s string) []byte { return append([]byte(s), 0) }

// append64 appends a 64-bit attribute, first inserting the pad attribute the
// kernel uses (nla_put_64bit) to keep the value 8-byte aligned -- libnftnl, which
// iptables uses, requires it. The value lands at 16 (nlmsghdr) + len(b) + 4.
func append64(b []byte, typ, pad uint16, v uint64) []byte {
	if (nlmsgHdrLen+len(b)+4)%8 != 0 {
		b = append(b, rtattr(pad, nil)...)
	}
	return append(b, nftBE64(typ, v)...)
}

func (t *nftTable) tableMsg(flags uint16, seq, pid uint32) []byte {
	b := []byte{t.family, 0, 0, 0}
	b = append(b, rtattr(nftaTableName, cstrz(t.name))...)
	b = append(b, nftAttrBE32(nftaTableUse, uint32(len(t.chains)))...)
	b = append64(b, nftaTableHandle, nftaTablePad, t.handle)
	return nlMsg(uint16(nfnlSubsysNftables<<8|nftMsgNewTable), flags, seq, pid, b)
}

func (t *nftTable) chainMsg(c *nftChain, flags uint16, seq, pid uint32) []byte {
	b := []byte{t.family, 0, 0, 0}
	b = append(b, rtattr(nftaChainTable, cstrz(t.name))...)
	b = append(b, rtattr(nftaChainName, cstrz(c.name))...)
	b = append64(b, nftaChainHandle, nftaChainPad, c.handle)
	if c.hooked {
		hook := append(nftAttrBE32(nftaHookHooknum, uint32(c.hooknum)), nftAttrBE32(nftaHookPriority, uint32(c.prio))...)
		b = append(b, rtattr(nftaChainHook|nlaFNested, hook)...)
		b = append(b, nftAttrBE32(nftaChainPolicy, c.policy)...)
		kind := c.kind
		if kind == "" {
			kind = "filter"
		}
		b = append(b, rtattr(nftaChainType, cstrz(kind))...)
	}
	b = append(b, nftAttrBE32(nftaChainUse, uint32(len(c.rules)))...)
	return nlMsg(uint16(nfnlSubsysNftables<<8|nftMsgNewChain), flags, seq, pid, b)
}

func (t *nftTable) ruleMsg(c *nftChain, r *nftRule, flags uint16, seq, pid uint32) []byte {
	b := []byte{t.family, 0, 0, 0}
	b = append(b, rtattr(nftaRuleTable, cstrz(t.name))...)
	b = append(b, rtattr(nftaRuleChain, cstrz(c.name))...)
	b = append64(b, nftaRuleHandle, nftaRulePad, r.handle)
	if len(r.exprs) > 0 {
		b = append(b, rtattr(nftaRuleExpressions|nlaFNested, r.exprs)...)
	}
	return nlMsg(uint16(nfnlSubsysNftables<<8|nftMsgNewRule), flags, seq, pid, b)
}

func (t *nftTable) setMsg(st *nftSet, flags uint16, seq, pid uint32) []byte {
	b := []byte{t.family, 0, 0, 0}
	b = append(b, rtattr(nftaSetTable, cstrz(t.name))...)
	b = append(b, rtattr(nftaSetName, cstrz(st.name))...)
	b = append(b, nftBE64(nftaSetHandle, st.handle)...)
	return nlMsg(uint16(nfnlSubsysNftables<<8|nftMsgNewSet), flags, seq, pid, b)
}

func nftDone(seq, pid uint32) []byte {
	return nlMsg(nlmsgDone, nlmFMulti, seq, pid, make([]byte, 4))
}

// nftBE64 is an 8-byte big-endian attribute (nftables handles are network order).
func nftBE64(typ uint16, v uint64) []byte {
	b := make([]byte, 8)
	for i := 0; i < 8; i++ {
		b[7-i] = byte(v >> (8 * i))
	}
	return rtattr(typ, b)
}

// nftCompatReply answers an NFT_COMPAT_GET by echoing the extension's name,
// revision and type -- i.e. confirming the requested revision is available, as
// the kernel does. body is the request after the netlink header.
func nftCompatReply(seq, pid uint32, body []byte) []byte {
	b := []byte{0, 0, 0, 0} // nfgenmsg
	if len(body) >= nfgenmsgLen {
		b[0] = body[0]
		at := parseNlAttrs(body[nfgenmsgLen:])
		if n := at[nftaCompatName]; n != nil {
			b = append(b, rtattr(nftaCompatName, n)...)
		}
		if r := at[nftaCompatRev]; r != nil {
			b = append(b, rtattr(nftaCompatRev, r)...)
		}
		if t := at[nftaCompatType]; t != nil {
			b = append(b, rtattr(nftaCompatType, t)...)
		}
	}
	return nlMsg(uint16(nfnlSubsysNftCompat<<8|nfnlMsgCompatNew), 0, seq, pid, b)
}

// nftGenMsg builds an NFT_MSG_NEWGEN reply carrying the ruleset generation id.
func nftGenMsg(gen, seq, pid uint32) []byte {
	body := make([]byte, nfgenmsgLen)
	body[0] = 0 // AF_UNSPEC
	body = append(body, nftAttrBE32(nftaGenID, gen)...)
	return nlMsg(uint16(nfnlSubsysNftables<<8|nftMsgNewGen), 0, seq, pid, body)
}

// nftAttrBE32 is a 4-byte attribute whose value is big-endian (nftables stores
// integers in network byte order).
func nftAttrBE32(typ uint16, v uint32) []byte {
	b := []byte{0, 0, 0, 0}
	b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	return rtattr(typ, b)
}

func be32(b []byte) uint32 {
	return uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
}

func be64(b []byte) uint64 {
	return uint64(be32(b[4:])) | uint64(be32(b))<<32
}
