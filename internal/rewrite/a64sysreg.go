package rewrite

// A64SysregSites returns, in ascending order, the byte offsets of every
// system-register access the stub rewrites -- mrs/msr tpidr_el0 and mrs
// ctr_el0 (see IsA64Sysreg) -- by a linear scan of code. These encodings are
// distinctive enough that a linear scan is safe (unlike svc, they never
// collide with data or ordinary instructions) and necessary: a big C++ binary
// reaches most of them only through indirect branches recursive descent never
// follows (Node has ~550 tpidr reads, a third unreachable by descent).
//
// The scan is a flat pass over every 4-byte word, run once per binary at
// cold-cache startup over the whole executable image, so on arm64 it takes a
// NEON fast path (a64sysreg_arm64.s) that rejects 4 words at a time; matches
// are rare, so almost every block is skipped. The scalar path here is always
// correct and is what every other architecture uses.
func A64SysregSites(code []byte) []int {
	if sites, ok := a64sysregVec(code); ok {
		return sites
	}
	return a64sysregScalar(code)
}

func a64sysregScalar(code []byte) []int {
	n := len(code) &^ 3
	var sites []int
	for off := 0; off+4 <= n; off += 4 {
		if IsA64Sysreg(le32(code[off:])) {
			sites = append(sites, off)
		}
	}
	return sites
}
