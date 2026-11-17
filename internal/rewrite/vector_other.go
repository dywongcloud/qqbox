//go:build !amd64 && !arm64

package rewrite

const simdName = "scalar" // no SIMD scan kernel for this architecture
