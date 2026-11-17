package rewrite

import (
	"slices"
	"testing"
)

// Sysreg words placed among NOP filler at offsets that cover every lane of a
// 16-byte NEON block (0..3), both words of adjacent blocks, and the tail, must
// be found exactly -- by the NEON path and, forced off, by the scalar path.
func TestA64SysregSitesSynthetic(t *testing.T) {
	const nop = 0xd503201f
	words := make([]uint32, 403) // 403*4 = 1612 bytes: 100 full blocks + a 3-word tail
	for i := range words {
		words[i] = nop
	}
	targets := []uint32{a64MrsTpidr | 5, a64MsrTpidr | 9, a64MrsCtr | 0x1f}
	idxs := []int{0, 1, 2, 3, 4, 7, 8, 16, 17, 31, 64, 100, 255, 256, 399, 400, 402}
	var want []int
	for k, idx := range idxs {
		words[idx] = targets[k%3] // keep the register field, vary the op
		want = append(want, idx*4)
	}
	slices.Sort(want)
	code := make([]byte, len(words)*4)
	for i, w := range words {
		putLE32(code[i*4:], w)
	}

	SetVectorScan(true)
	if got := A64SysregSites(code); !slices.Equal(got, want) {
		t.Fatalf("vector path: got %v\nwant %v (level=%s)", got, want, VectorScanLevel())
	}
	SetVectorScan(false)
	if got := A64SysregSites(code); !slices.Equal(got, want) {
		t.Fatalf("scalar path: got %v\nwant %v", got, want)
	}
	SetVectorScan(true)
	t.Logf("found %d sysreg sites, level=%s", len(want), VectorScanLevel())
}

// On real arm64 code the two paths must agree (the differential; on amd64 both
// are the scalar path, which still checks the API).
func TestA64SysregSitesMatchesScalar(t *testing.T) {
	code, _ := buildA64Text(t)
	SetVectorScan(true)
	got := A64SysregSites(code)
	want := a64sysregScalar(code)
	if !slices.Equal(got, want) {
		t.Fatalf("vector vs scalar differ: %d vs %d sites", len(got), len(want))
	}
	t.Logf("matched %d sysreg sites across %d bytes of real arm64 code (level=%s)",
		len(want), len(code), VectorScanLevel())
}

func BenchmarkA64SysregSites(b *testing.B) {
	code := benchA64Code()
	run := func(b *testing.B) {
		b.SetBytes(int64(len(code)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = A64SysregSites(code)
		}
	}
	b.Run("scalar", func(b *testing.B) { SetVectorScan(false); run(b); SetVectorScan(true) })
	b.Run("vector", func(b *testing.B) { SetVectorScan(true); run(b) })
}
