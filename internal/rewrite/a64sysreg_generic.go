//go:build !arm64

package rewrite

// a64sysregVec reports no SIMD scan: only arm64 has the NEON kernel (and arm64
// sysregs appear only in arm64 guests, which run on arm64 hosts).
func a64sysregVec(code []byte) ([]int, bool) { return nil, false }
