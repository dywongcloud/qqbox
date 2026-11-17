//go:build linux

package sandbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// smapsOf is process p's mappings (hostMaps) with the host kernel's smaps
// lines for each: they are the stub's own mappings, so the kernel counts
// them as it counts the guest's. A mapping hostMaps splits out of a host one
// (the stack, which the host merges with a neighbour the guest maps against
// it with MAP_FIXED; the guard gap keeps others away) is counted from
// /proc/<pid>/pagemap (pagemapStat).
func (g *dguest) smapsOf(p *dguest) ([]mapEnt, []vmaStat, bool) {
	es, ok := p.hostMaps()
	if !ok {
		return nil, nil, false
	}
	pid := p.mp.pid.Load()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps", pid))
	if err != nil {
		return nil, nil, false
	}
	type hostVMA struct{ lo, hi uint64 }
	host := map[hostVMA]string{}
	var cur hostVMA
	var body strings.Builder
	flush := func() {
		if cur.hi != 0 {
			host[cur] = body.String()
		}
		body.Reset()
	}
	for line := range strings.Lines(string(b)) {
		var lo, hi uint64
		var perms string
		if n, _ := fmt.Sscanf(line, "%x-%x %s", &lo, &hi, &perms); n == 3 && len(perms) == 4 {
			flush()
			cur = hostVMA{lo, hi}
			continue
		}
		body.WriteString(line)
	}
	flush()
	st := make([]vmaStat, len(es))
	for i, e := range es {
		if lines, ok := host[hostVMA{e.lo, e.hi}]; ok {
			st[i] = parseSmapsBody(lines)
		} else {
			st[i] = pagemapStat(pid, e)
		}
	}
	return es, st, true
}

// pagemapStat counts mapping e of host process pid from its pagemap: a page
// present is resident, one swapped out is swap, one not exclusively mapped is
// shared, and one present that is no file page is anonymous, which Linux
// holds dirty. The pagemap does not tell an unprivileged reader how many
// processes share a page, so a shared page counts whole in Pss here, nor
// whether a page was referenced, so a resident page counts so.
func pagemapStat(pid int64, e mapEnt) vmaStat {
	var s vmaStat
	f, err := os.Open(fmt.Sprintf("/proc/%d/pagemap", pid))
	if err != nil {
		return s
	}
	defer f.Close()
	n := (e.hi - e.lo) / pageSize
	buf := make([]byte, 8*n)
	if _, err := f.ReadAt(buf, int64(e.lo/pageSize*8)); err != nil {
		return s
	}
	for i := uint64(0); i < n; i++ {
		v := binary.LittleEndian.Uint64(buf[8*i:])
		present, swapped, file, excl := v>>63&1 != 0, v>>62&1 != 0, v>>61&1 != 0, v>>56&1 != 0
		switch {
		case swapped:
			s.swap += pageSize
			s.swapPss += pageSize
		case !present:
		case !file:
			s.anon += pageSize
			if excl {
				s.privateDirty += pageSize
			} else {
				s.sharedDirty += pageSize
			}
			s.pssDirty += pageSize
		case excl:
			s.privateClean += pageSize
		default:
			s.sharedClean += pageSize
		}
		if present {
			s.rss += pageSize
			s.referenced += pageSize
		}
	}
	s.pss = s.rss
	return s
}
