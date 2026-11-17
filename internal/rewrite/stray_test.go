package rewrite

import (
	"slices"
	"testing"
)

func TestStraySitesSynthetic(t *testing.T) {
	code := make([]byte, 300)
	for i := range code {
		code[i] = 0x90 // filler that is neither 0x0f nor 0x05
	}
	// Isolated pairs across lane positions and block boundaries, plus an
	// adjacent run (50,52) and a pair straddling the 16-byte block (15,16).
	put := func(o int) { code[o], code[o+1] = 0x0f, 0x05 }
	offs := []int{0, 1, 15, 16, 17, 31, 32, 50, 52, 100, 200, 298}
	for _, o := range offs {
		put(o)
	}
	// Re-derive the ground truth with a byte loop so overlaps are counted the
	// same way straySites must count them.
	want := strayScalarRef(code)

	SetVectorScan(true)
	if got := straySites(code); !slices.Equal(got, want) {
		t.Fatalf("vector: got %v\nwant %v (level=%s)", got, want, VectorScanLevel())
	}
	SetVectorScan(false)
	if got := straySites(code); !slices.Equal(got, want) {
		t.Fatalf("scalar: got %v\nwant %v", got, want)
	}
	SetVectorScan(true)
	t.Logf("found %d pairs, level=%s", len(want), VectorScanLevel())
}

// strayScalarRef is an independent reference, so the test does not grade the
// scalar path against itself.
func strayScalarRef(code []byte) []int {
	var out []int
	for i := 0; i+1 < len(code); i++ {
		if code[i] == 0x0f && code[i+1] == 0x05 {
			out = append(out, i)
		}
	}
	return out
}

func TestStraySitesMatchesScalar(t *testing.T) {
	code, _ := realX86TextEntries(t)
	SetVectorScan(true)
	got := straySites(code)
	want := strayScalarRef(code)
	if !slices.Equal(got, want) {
		t.Fatalf("vector vs scalar differ: %d vs %d pairs", len(got), len(want))
	}
	t.Logf("matched %d 0f05 pairs across %d bytes (level=%s)", len(want), len(code), VectorScanLevel())
}

func BenchmarkStraySites(b *testing.B) {
	code, _ := realX86TextEntries(b)
	run := func(b *testing.B) {
		b.SetBytes(int64(len(code)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = straySites(code)
		}
	}
	b.Run("scalar", func(b *testing.B) { SetVectorScan(false); run(b); SetVectorScan(true) })
	b.Run("vector", func(b *testing.B) { SetVectorScan(true); run(b) })
}
