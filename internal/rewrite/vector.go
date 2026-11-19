package rewrite

import (
	"os"
	"sync"
	"sync/atomic"
)

// Most of the rewriter's work is recursive descent, which is sequential (each
// instruction's length gates the next) and does not vectorize. But a couple of
// steps are flat scans over the whole executable image, run once per binary at
// cold-cache startup, and those do: the arm64 sysreg scan (a64sysreg_arm64.s,
// NEON) and the x86 stray-syscall scan (stray_amd64.s, SSE2). Each uses its
// architecture's baseline SIMD -- NEON and SSE2 are mandatory parts of the
// AArch64 and x86-64 ISAs -- so the kernel is chosen by build tag and there is
// nothing to detect. The scalar path is always correct.
//
// The only control is an on/off switch, for benchmarking the two paths and for
// a host whose SIMD is suspect. It defaults on; QQBOX_NO_VECTOR=1 in the
// environment turns it off before the first scan.

var (
	vecOnce sync.Once
	vecOff  atomic.Bool // the switch; false means enabled
)

func vecInit() {
	vecOnce.Do(func() {
		if v, ok := os.LookupEnv("QQBOX_NO_VECTOR"); ok && v != "0" && v != "" {
			vecOff.Store(true)
		}
	})
}

// vecEnabled reports whether the SIMD scan kernels should be used.
func vecEnabled() bool {
	vecInit()
	return !vecOff.Load()
}

// SetVectorScan enables or disables the vectorized startup scans (default
// enabled), forcing the scalar path when off. It takes effect on the next scan
// and is process-wide, not per sandbox.
func SetVectorScan(on bool) {
	vecInit()
	vecOff.Store(!on)
}

// VectorScanLevel names the scan kernel in effect: the architecture's baseline
// SIMD ("neon"/"sse2") when enabled, else "scalar".
func VectorScanLevel() string {
	if !vecEnabled() {
		return "scalar"
	}
	return simdName
}
