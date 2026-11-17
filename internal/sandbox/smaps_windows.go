package sandbox

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// smapsOf is the caller's mappings (hostMaps) with their pages' counts from
// the host's working set of the stub process (QueryWorkingSetEx, read as
// wsBits). A page in the working set is resident; so is one out of it that
// the kernel still holds in memory (a page trimmed to its standby list, or
// one a fork cloned that another process holds and this one has not touched
// since), which Linux keeps mapped; one in the paging file is swap. One more
// than one process holds is shared, its Pss divided by how many (a cloned
// page out of the working set by two); one locked is locked. Windows does not
// say whether a page was written, so a resident page of a private mapping,
// anonymous memory here (file contents are copied in), counts dirty, as
// Linux holds anonymous memory, and one of a shared view clean; nor whether
// it was referenced, so a resident page counts so. Another process's
// mappings are the supervisor's model, without counts.
func (g *dguest) smapsOf(p *dguest) ([]mapEnt, []vmaStat, bool) {
	if p.tgid != g.tgid || g.mp.w == nil {
		return nil, nil, false
	}
	es, ok := g.hostMaps()
	if !ok {
		return nil, nil, false
	}
	proc := g.mp.w.sp.proc
	st := make([]vmaStat, len(es))
	for i, e := range es {
		st[i] = workingSet(proc, e)
	}
	return es, st, true
}

// wsInfo is a PSAPI_WORKING_SET_EX_INFORMATION: a page's address, and what
// QueryWorkingSetEx says of it.
type wsInfo struct {
	addr  uintptr
	attrs uint64
}

// wsBits reads a working-set block (MEMORY_WORKING_SET_EX_BLOCK; x/sys's
// accessors test every bit but the first against 1): in the working set
// (valid), how many processes share it, shareable, locked, and for a page out
// of it where it is (1 in memory, 2 in the paging file).
func wsBits(b uint64) (valid bool, share uint64, shared, locked bool, where uint64) {
	valid, shared = b&1 != 0, b>>15&1 != 0
	if valid {
		return true, b >> 1 & 7, shared, b>>22&1 != 0, 1
	}
	return false, 0, shared, false, b >> 22 & 3
}

// workingSet counts mapping e's committed pages in process proc's working
// set (see smapsOf).
func workingSet(proc windows.Handle, e mapEnt) vmaStat {
	var s vmaStat
	info := make([]wsInfo, 1024)
	dirty := e.attr&8 == 0
	anon := dirty && (e.attr>>8 == mapAnon || e.attr>>8 == mapHeap || e.attr>>8 == mapStack)
	for a := e.lo; a < e.hi; {
		var mbi windows.MemoryBasicInformation
		if windows.VirtualQueryEx(proc, uintptr(a), &mbi, unsafe.Sizeof(mbi)) != nil {
			break
		}
		end := min(uint64(mbi.BaseAddress)+uint64(mbi.RegionSize), e.hi)
		if mbi.State != windows.MEM_COMMIT {
			a = end
			continue
		}
		for a < end {
			n := min((end-a)/pageSize, uint64(len(info)))
			for j := range n {
				info[j] = wsInfo{addr: uintptr(a + j*pageSize)}
			}
			if windows.QueryWorkingSetEx(proc, uintptr(unsafe.Pointer(&info[0])), uint32(n)*uint32(unsafe.Sizeof(info[0]))) != nil {
				return s
			}
			for _, w := range info[:n] {
				valid, c, shared, locked, where := wsBits(w.attrs)
				switch {
				case where == 2:
					s.swap += pageSize
					s.swapPss += pageSize
					continue
				case where != 1:
					continue
				case !valid && shared:
					c = 2
				}
				s.rss += pageSize
				s.referenced += pageSize
				if locked {
					s.locked += pageSize
				}
				pss := uint64(pageSize)
				if c > 1 {
					pss = pageSize / c
					if dirty {
						s.sharedDirty += pageSize
					} else {
						s.sharedClean += pageSize
					}
				} else if dirty {
					s.privateDirty += pageSize
				} else {
					s.privateClean += pageSize
				}
				s.pss += pss
				if dirty {
					s.pssDirty += pss
				}
				if anon {
					s.anon += pageSize
				}
			}
			a += n * pageSize
		}
	}
	return s
}
