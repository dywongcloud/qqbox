package rewrite

import "testing"

// a 1 MiB arm64 code buffer: mostly NOPs (descent flows straight through),
// with a svc and a tpidr access every 256 bytes.
func benchA64Code() []byte {
	const n = 1 << 20
	code := make([]byte, n)
	for off := 0; off < n; off += 4 {
		putLE32(code[off:], 0xd503201f) // nop
	}
	for off := 0; off+4 <= n; off += 256 {
		putLE32(code[off:], a64SVC0)
		putLE32(code[off+64:], a64MrsTpidr|0x3) // mrs x3, tpidr_el0
	}
	putLE32(code[n-4:], a64RET)
	return code
}

func BenchmarkA64Sites(b *testing.B) {
	code := benchA64Code()
	b.SetBytes(int64(len(code)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		A64Sites(code, []int{0})
	}
}

// the linear sysreg scan (as sandbox.computeSitesELF runs it).
func BenchmarkA64SysregScan(b *testing.B) {
	code := benchA64Code()
	b.SetBytes(int64(len(code)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sites []int
		for off := 0; off+4 <= len(code); off += 4 {
			if IsA64Sysreg(le32(code[off:])) {
				sites = append(sites, off)
			}
		}
		_ = sites
	}
}

// a 1 MiB x86-64 code buffer: mostly NOPs, a syscall every 256 bytes, ret last.
func benchX86Code() []byte {
	const n = 1 << 20
	code := make([]byte, n)
	for i := range code {
		code[i] = 0x90 // nop
	}
	for off := 0; off+2 <= n; off += 256 {
		code[off], code[off+1] = 0x0f, 0x05 // syscall
	}
	code[n-1] = 0xc3 // ret
	return code
}

func BenchmarkX86Analyze(b *testing.B) {
	code := benchX86Code()
	b.SetBytes(int64(len(code)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Analyze(code)
	}
}
