package sandbox

// smapEntSize is a stub/macos/smaps.zig SmapEnt: a mapping (40 bytes) and its
// nine counts.
const smapEntSize = 40 + 9*8

// smapsOf is process p's mappings with their counts, from its stub
// (stub/macos/smaps.zig): the caller's own over its channel, another's from
// its stub's service thread.
func (g *dguest) smapsOf(p *dguest) ([]mapEnt, []vmaStat, bool) {
	var raw []byte
	if p.tgid == g.tgid {
		for from := uint64(0); len(raw) < 1<<24; {
			g.ch.page.args = [6]uint64{from}
			g.ch.reply(repSmaps)
			if err := g.ch.wait(); err != nil || g.ch.page.kind != msgHostRet {
				return nil, nil, false
			}
			n := int(g.ch.page.ret)
			sc := g.scratch()
			if n*smapEntSize > len(sc) {
				return nil, nil, false
			}
			raw = append(raw, sc[:n*smapEntSize]...)
			if from = g.ch.page.args[1]; from == 0 {
				break
			}
		}
	} else {
		fs := g.dfs
		if fs.run == nil || fs.run.broker == nil {
			return nil, nil, false
		}
		fs.mu.Lock()
		dp := fs.procs[p.tgid]
		fs.mu.Unlock()
		if dp == nil || dp.ospid <= 0 {
			return nil, nil, false
		}
		pid := dp.ospid
		var ok bool
		if raw, ok = fs.run.broker.smapsOf(pid, func() bool { return fs.stubDone(pid) }); !ok {
			return nil, nil, false
		}
	}
	return smapsRecords(raw)
}

// smapsRecords reads SmapEnt records. A resident page of an anonymous
// mapping is anonymous, and so is one a private file mapping copied when it
// was written.
func smapsRecords(raw []byte) ([]mapEnt, []vmaStat, bool) {
	n := len(raw) / smapEntSize
	es, st := make([]mapEnt, n), make([]vmaStat, n)
	for i := range es {
		r := raw[i*smapEntSize:]
		es[i] = mapEnt{le64(r), le64(r[8:]), le64(r[16:]), le64(r[24:]), le64(r[32:])}
		c := r[40:]
		s := vmaStat{rss: le64(c), pss: le64(c[8:]), pssDirty: le64(c[16:]), sharedClean: le64(c[24:]), sharedDirty: le64(c[32:]),
			privateClean: le64(c[40:]), privateDirty: le64(c[48:]), referenced: le64(c[56:]), swap: le64(c[64:])}
		switch k := es[i].attr >> 8; {
		case es[i].attr&8 != 0:
		case k == mapAnon || k == mapHeap || k == mapStack:
			s.anon = s.rss
		default:
			s.anon = s.privateDirty
		}
		st[i] = s
	}
	return es, st, true
}
