//go:build arm64

package rewrite

// a64sysregNextHit returns the offset of the first 16-byte block in p[from:n]
// that holds a sysreg word (or, in the final <16-byte tail, the matching word
// itself), else n. p, from and n are multiples of 4 and from+... stays within
// the slice the caller passed. Implemented in a64sysreg_arm64.s (NEON).
//
//go:noescape
func a64sysregNextHit(p *byte, n, from int) int

// a64sysregVec is the NEON fast path: it skips blocks with no sysreg word four
// words at a time and re-checks only a block the kernel flagged, scalar, so the
// result is identical to a64sysregScalar.
func a64sysregVec(code []byte) ([]int, bool) {
	if !vecEnabled() {
		return nil, false
	}
	n := len(code) &^ 3
	if n == 0 {
		return nil, true // NEON owns it; nothing to scan
	}
	var sites []int
	for off := 0; off < n; {
		h := a64sysregNextHit(&code[0], n, off)
		if h >= n {
			break
		}
		end := min(h+16, n)
		for i := h; i+4 <= end; i += 4 {
			if IsA64Sysreg(le32(code[i:])) {
				sites = append(sites, i)
			}
		}
		off = h + 16
	}
	return sites, true
}
