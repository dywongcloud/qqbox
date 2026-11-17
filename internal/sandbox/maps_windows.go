//go:build windows

package sandbox

import (
	"sort"
	"unsafe"

	"golang.org/x/sys/windows"
)

// hostMaps lists the guest's mappings: the stub's mmap area and heap, and
// the images and main stack the supervisor placed, split where the page
// protection changes.
// otherMaps has nothing to add for another process: its mappings are read the
// usual way.
func (g *dguest) otherMaps(p *dguest) ([]mapEnt, bool) { return nil, false }

func (g *dguest) hostMaps() ([]mapEnt, bool) {
	es, ok := g.mapsOfStub()
	if !ok {
		return nil, false
	}
	w := g.mp.w
	img := g.imgInfoOf(g)
	if img == nil || w == nil {
		return es, true
	}
	add := func(lo, hi, off, kind uint64) {
		for a := lo; a < hi; {
			var mbi windows.MemoryBasicInformation
			if windows.VirtualQueryEx(w.sp.proc, uintptr(a), &mbi, unsafe.Sizeof(mbi)) != nil {
				return
			}
			end := min(uint64(mbi.BaseAddress)+uint64(mbi.RegionSize), hi)
			if mbi.State != memFree {
				prot := uint64(0)
				if mbi.State == windows.MEM_COMMIT {
					prot = linuxProt(mbi.Protect)
				}
				es = append(es, mapEnt{lo: a, hi: end, off: off + a - lo, attr: kind<<8 | prot})
			}
			a = end
		}
	}
	for _, s := range w.segs {
		kind := s.attr
		if kind == mapFile {
			kind = mapMain
			if s.lo >= img.interpLo && s.hi <= img.interpHi {
				kind = mapInterp
			}
		}
		add(s.lo, s.hi, s.off, kind)
	}
	add(img.stackLo, img.stackHi, 0, mapStack)
	sort.Slice(es, func(i, j int) bool { return es[i].lo < es[j].lo })
	return es, true
}

const memFree = 0x10000

// linuxProt is the Linux PROT_* form of a Windows page protection.
func linuxProt(p uint32) uint64 {
	switch p &^ (windows.PAGE_GUARD | windows.PAGE_NOCACHE | windows.PAGE_WRITECOMBINE) {
	case windows.PAGE_READONLY:
		return 1
	case windows.PAGE_READWRITE, windows.PAGE_WRITECOPY:
		return 3
	case windows.PAGE_EXECUTE:
		return 4
	case windows.PAGE_EXECUTE_READ:
		return 5
	case windows.PAGE_EXECUTE_READWRITE, windows.PAGE_EXECUTE_WRITECOPY:
		return 7
	}
	return 0
}
