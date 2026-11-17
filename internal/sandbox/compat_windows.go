package sandbox

// mincore on Windows, where the stub owns the guest's address space but does
// not serve it: the supervisor answers it so a guest that probes page
// residency is served, not ENOSYS. (membarrier is the stub's: thread.zig.)

func init() {
	// mincore reports every page of the range resident. The sandbox does not
	// evict guest pages, so "resident" is a faithful-enough answer; the call
	// still validates its arguments as Linux does.
	regSys(lxMincore, func(g *dguest, a [6]uint64) int64 {
		addr, length, vec := a[0], a[1], a[2]
		if addr&(pageSize-1) != 0 {
			return sErr(lxEINVAL)
		}
		if length == 0 {
			return 0
		}
		pages := (length + pageSize - 1) / pageSize
		b := make([]byte, pages)
		for i := range b {
			b[i] = 1
		}
		if g.writeMem(vec, b) != nil {
			return sErr(lxEFAULT)
		}
		return 0
	})
}
