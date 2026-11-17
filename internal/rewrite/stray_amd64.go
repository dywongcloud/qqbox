//go:build amd64

package rewrite

// strayNextHit returns the offset of the first `0f 05` pair in p[from:n], else
// n. It reads 16 bytes plus one for the overlap, so the caller keeps from < n.
// Implemented in stray_amd64.s (SSE2, part of the x86-64 baseline).
//
//go:noescape
func strayNextHit(p *byte, n, from int) int

func strayVec(code []byte) ([]int, bool) {
	if !vecEnabled() {
		return nil, false
	}
	n := len(code)
	if n < 2 {
		return nil, true
	}
	var out []int
	for off := strayNextHit(&code[0], n, 0); off < n; off = strayNextHit(&code[0], n, off+1) {
		out = append(out, off)
	}
	return out, true
}
