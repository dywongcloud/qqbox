//go:build darwin || linux || windows

package sandbox

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// /proc/<pid>/smaps and smaps_rollup: each mapping of /proc/<pid>/maps with
// what the memory behind it holds. The counts are the host's for that same
// memory (smapsOf): the kernel's own lines on Linux, where the guest's
// mappings are the stub's; on macOS and Windows, its pages' residency,
// dirtiness and sharing as their kernels report them, a page shared with
// another process (after a fork, or of a shared mapping) counted shared.

// vmaStat is what smaps says of one mapping beyond its maps line, in bytes.
type vmaStat struct {
	rss, pss, pssDirty                     uint64
	sharedClean, sharedDirty               uint64
	privateClean, privateDirty, referenced uint64
	anon, swap, swapPss, locked            uint64
	host                                   string // the host kernel's own lines for it (Linux), used as they are
}

// smapsFields are the counts of a Linux smaps entry, as named there.
var smapsFields = []string{"Rss", "Pss", "Pss_Dirty", "Shared_Clean", "Shared_Dirty", "Private_Clean", "Private_Dirty",
	"Referenced", "Anonymous", "KSM", "LazyFree", "AnonHugePages", "ShmemPmdMapped", "FilePmdMapped",
	"Shared_Hugetlb", "Private_Hugetlb", "Swap", "SwapPss", "Locked"}

func (s *vmaStat) field(name string) *uint64 {
	switch name {
	case "Rss":
		return &s.rss
	case "Pss":
		return &s.pss
	case "Pss_Dirty":
		return &s.pssDirty
	case "Shared_Clean":
		return &s.sharedClean
	case "Shared_Dirty":
		return &s.sharedDirty
	case "Private_Clean":
		return &s.privateClean
	case "Private_Dirty":
		return &s.privateDirty
	case "Referenced":
		return &s.referenced
	case "Anonymous":
		return &s.anon
	case "Swap":
		return &s.swap
	case "SwapPss":
		return &s.swapPss
	case "Locked":
		return &s.locked
	}
	return nil
}

// parseSmapsBody reads the counts of a host smaps entry's lines.
func parseSmapsBody(lines string) vmaStat {
	s := vmaStat{host: lines}
	for _, l := range strings.Split(lines, "\n") {
		name, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		if p := s.field(name); p != nil {
			if v, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64); err == nil {
				*p = v << 10
			}
		}
	}
	return s
}

// pidSmaps is /proc/<pid>/smaps of process p, or with rollup its
// smaps_rollup.
func (g *dguest) pidSmaps(p *dguest, rollup bool) []byte {
	page := uint64(g.guestPageSize())
	es, st, ok := g.smapsOf(p)
	var lines []string
	if ok {
		lines = g.mapsLines(p, es)
	} else {
		// Mappings only the supervisor's model knows: listed without counts.
		lines = strings.Split(strings.TrimSuffix(string(g.pidMaps(p)), "\n"), "\n")
		es, st = make([]mapEnt, 0, len(lines)), make([]vmaStat, len(lines))
		for _, l := range lines {
			var e mapEnt
			var perm string
			fmt.Sscanf(l, "%x-%x %s", &e.lo, &e.hi, &perm)
			for i, c := range []byte("rwxs") {
				if len(perm) > i && perm[i] == c {
					e.attr |= 1 << i
				}
			}
			es = append(es, e)
		}
	}
	var b bytes.Buffer
	if rollup {
		// Linux sums the process's mappings, not the gate page (vsyscall),
		// which maps and smaps list after them.
		for len(es) > 0 && es[len(es)-1].attr>>8 == mapVsyscall {
			es, st = es[:len(es)-1], st[:len(st)-1]
		}
		var sum vmaStat
		var pssAnon, pssFile, pssShmem uint64
		for i, e := range es {
			for _, f := range smapsFields {
				if d := sum.field(f); d != nil {
					*d += *st[i].field(f)
				}
			}
			switch k := e.attr >> 8; {
			case e.attr&8 != 0 || k == mapShm:
				pssShmem += st[i].pss
			case k == mapFile || k == mapMain || k == mapInterp:
				pssFile += st[i].pss
			default:
				pssAnon += st[i].pss
			}
		}
		var lo, hi uint64
		if len(es) > 0 {
			lo, hi = es[0].lo, es[len(es)-1].hi
		}
		head := fmt.Sprintf("%08x-%08x ---p 00000000 00:00 0 ", lo, hi)
		b.WriteString(head + strings.Repeat(" ", max(0, 73-len(head))) + "[rollup]\n")
		for _, f := range smapsFields {
			switch f {
			case "Pss_Dirty":
				smapsKB(&b, f, sum.pssDirty)
				smapsKB(&b, "Pss_Anon", pssAnon)
				smapsKB(&b, "Pss_File", pssFile)
				smapsKB(&b, "Pss_Shmem", pssShmem)
			default:
				if p := sum.field(f); p != nil {
					smapsKB(&b, f, *p)
				} else {
					smapsKB(&b, f, 0)
				}
			}
		}
		return b.Bytes()
	}
	for i, e := range es {
		b.WriteString(lines[i] + "\n")
		if st[i].host != "" {
			b.WriteString(st[i].host)
			continue
		}
		smapsKB(&b, "Size", e.hi-e.lo)
		smapsKB(&b, "KernelPageSize", page)
		smapsKB(&b, "MMUPageSize", page)
		for _, f := range smapsFields {
			if p := st[i].field(f); p != nil {
				smapsKB(&b, f, *p)
			} else {
				smapsKB(&b, f, 0)
			}
		}
		b.WriteString("THPeligible:           0\n")
		b.WriteString("VmFlags: " + vmFlags(e) + "\n")
	}
	return b.Bytes()
}

func smapsKB(b *bytes.Buffer, name string, v uint64) {
	fmt.Fprintf(b, "%-16s%8d kB\n", name+":", v>>10)
}

// vmFlags is the VmFlags of a mapping as Linux lists them, in its order:
// the protection, what mprotect may give it, a shared mapping's may-share,
// the stack's grows-down, a private writable mapping's accounting, and
// soft-dirty, which a new mapping has.
func vmFlags(e mapEnt) string {
	var f []string
	r, w, x, shared := e.attr&1 != 0, e.attr&2 != 0, e.attr&4 != 0, e.attr&8 != 0
	k := e.attr >> 8
	for _, c := range []struct {
		on   bool
		name string
	}{
		{r, "rd"}, {w, "wr"}, {x, "ex"}, {shared, "sh"}, {true, "mr"}, {!shared || w, "mw"}, {true, "me"},
		{shared, "ms"}, {k == mapStack, "gd"}, {!shared && (w || k == mapAnon || k == mapHeap || k == mapStack), "ac"}, {true, "sd"},
	} {
		if c.on {
			f = append(f, c.name)
		}
	}
	return strings.Join(f, " ") + " "
}
