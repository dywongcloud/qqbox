package rewrite

import (
	"encoding/binary"
	"reflect"
	"testing"

	"golang.org/x/arch/arm64/arm64asm"
)

// put32 appends a little-endian 32-bit word.
func put32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// TestA64SitesSkipsDataInText builds: B +8 ; <svc-pattern data> ; svc #0 ; ret.
// Descent must follow the branch over the data word and find only the real svc.
func TestA64SitesSkipsDataInText(t *testing.T) {
	var code []byte
	code = put32(code, 0x14000002) // 0x00: b #8  (skip the next word)
	code = put32(code, a64SVC0)    // 0x04: svc-pattern DATA, unreachable
	code = put32(code, a64SVC0)    // 0x08: real svc #0
	code = put32(code, a64RET)     // 0x0c: ret

	sites := A64Sites(code, []int{0})
	if !reflect.DeepEqual(sites, []int{0x08}) {
		t.Fatalf("sites = %v, want [8] (the data word at 0x04 must be skipped)", sites)
	}

	A64Patch(code, sites)
	if got := le32(code[0x08:]); got != a64UDF0 {
		t.Fatalf("site not patched to udf: 0x%08x", got)
	}
	if got := le32(code[0x04:]); got != a64SVC0 {
		t.Fatalf("data word at 0x04 was wrongly patched: 0x%08x", got)
	}
}

// TestA64SegmentSitesStructural: an svc descent does not reach is code when
// what runs into it writes x8, however far back and by whatever instruction;
// not when an unconditional branch comes between, and never when decoded code
// loads it as data.
func TestA64SegmentSitesStructural(t *testing.T) {
	const nop, movX8, ldrX8 = 0xd503201f, 0xd2800ba8, 0xf94007e8 // nop; mov x8, #93; ldr x8, [sp, #8]
	var code []byte
	code = put32(code, a64RET) // 0x00: an entry
	code = put32(code, ldrX8)  // 0x04: a helper no entry reaches
	for i := 0; i < 40; i++ {
		code = put32(code, nop)
	}
	code = put32(code, a64SVC0)    // 0xa8: its syscall
	code = put32(code, a64RET)     // 0xac
	code = put32(code, 0x58000040) // 0xb0: an entry: ldr x0, .+8
	code = put32(code, a64RET)     // 0xb4
	code = put32(code, movX8)      // 0xb8: the 8 bytes it loads
	code = put32(code, a64SVC0)    // 0xbc
	code = put32(code, movX8)      // 0xc0: unreached
	code = put32(code, 0x14000002) // 0xc4: b .+8
	code = put32(code, a64SVC0)    // 0xc8: after an unconditional branch
	code = put32(code, a64RET)     // 0xcc
	sites := A64SegmentSites(code, 0, []uint64{0, 0xb0}, 0, uint64(len(code)))
	if !reflect.DeepEqual(sites, []int{0xa8}) {
		t.Fatalf("sites = %#x, want [0xa8]", sites)
	}
}

// TestA64SitesConditionalFallthrough: a B.cond falls through AND branches, so
// an svc on both the taken and fall-through paths is found.
func TestA64SitesConditionalFallthrough(t *testing.T) {
	var code []byte
	code = put32(code, 0x54000042) // 0x00: b.cs #8  (cond branch to 0x08)
	code = put32(code, a64SVC0)    // 0x04: svc on fall-through
	code = put32(code, a64SVC0)    // 0x08: svc on taken path
	code = put32(code, a64RET)     // 0x0c: ret
	sites := A64Sites(code, []int{0})
	if !reflect.DeepEqual(sites, []int{0x04, 0x08}) {
		t.Fatalf("sites = %v, want [4 8]", sites)
	}
}

// TestA64RewriterIsland checks a site becomes `b island`, the island enters
// the stub and its epilogue branches back past the svc; a site out of branch
// range falls back to udf.
func TestA64RewriterIsland(t *testing.T) {
	const base, vaddr, entry = 0x100000, 0x200000, 0x6f0000001000
	var code []byte
	code = put32(code, 0xd2800808) // mov x8, #64
	code = put32(code, a64SVC0)
	code = put32(code, a64RET)
	w := NewA64(base, 1<<20, entry)
	w.Apply([]int{4}, code, vaddr)
	if w.Fast != 1 || len(w.Sites) != 1 || w.Sites[0] != vaddr+4 {
		t.Fatalf("fast=%d sites=%x", w.Fast, w.Sites)
	}
	island := uint64(base + headerSize)
	if got := le32(code[4:]); got != encB(vaddr+4, island) {
		t.Fatalf("site = %#x, want b island", got)
	}
	tr := w.Trampolines()[headerSize:]
	if got := le32(tr[40:]); got != encB(island+40, vaddr+8) {
		t.Fatalf("epilogue branch = %#x", got)
	}
	if got := le32(tr[4:]); got != encADR(island+4, island+28, 16) {
		t.Fatalf("epilogue address = %#x", got)
	}
	if hi, lo := le32(tr[12:]), le32(tr[16:]); hi != encADRP(island+12, vaddr+8, 17) || lo>>10&0xfff != (vaddr+8)&0xfff {
		t.Fatalf("resume address = %#x %#x", hi, lo)
	}
	if lit := binary.LittleEndian.Uint64(tr[56:]); lit != entry {
		t.Fatalf("entry literal = %#x", lit)
	}
	far := NewA64(base, 1<<20, entry)
	code2 := put32(nil, a64SVC0)
	far.Apply([]int{0}, code2, base+1<<30)
	if far.Fast != 0 || le32(code2) != a64UDF0 {
		t.Fatalf("out-of-range site: fast=%d word=%#x", far.Fast, le32(code2))
	}
}

// Code wider than B's range takes an area on each side: each site's island
// goes in the area it reaches, and sites reach none only past both.
func TestA64TwoAreas(t *testing.T) {
	const lo, size, span = 0x10000000, 1 << 20, 150 << 20
	below, above := uint64(lo-size), uint64(lo+span)
	w := NewA64Areas([]A64Area{{below, size}, {above, size}}, 0x6f0000001000)
	near := put32(nil, a64SVC0)
	far := put32(nil, 0xd53bd041) // mrs x1, tpidr_el0
	w.Apply([]int{0}, near, lo)
	if left := w.Sysreg([]int{0}, far, lo+span-4, 0x1678, 0); len(left) != 0 {
		t.Fatalf("far site left: %x", left)
	}
	if w.Fast != 1 || le32(near) != encB(lo, below+headerSize) {
		t.Fatalf("near site %#x, want b %#x", le32(near), below+headerSize)
	}
	if le32(far) != encB(lo+span-4, above+headerSize) {
		t.Fatalf("far site %#x, want b %#x", le32(far), above+headerSize)
	}
	if a := w.Areas(); len(a[0]) != headerSize+a64IslandSize || len(a[1]) != headerSize+12 {
		t.Fatalf("area sizes %d %d", len(a[0]), len(a[1]))
	}
	beyond := put32(nil, a64SVC0)
	w.Apply([]int{0}, beyond, lo+span+(200<<20))
	if le32(beyond) != a64UDF0 {
		t.Fatalf("site past both areas %#x", le32(beyond))
	}
}

// A thread's exit keeps the trap: no island, which would write below a
// stack the thread may have given up.
func TestA64ExitKeepsTrap(t *testing.T) {
	code := put32(put32(nil, 0xd2800ba8), a64SVC0) // mov x8, #93; svc #0
	w := NewA64(0x100000, 1<<20, 0x6f0000001000)
	w.Apply([]int{4}, code, 0x200000)
	if w.Fast != 0 || len(w.Sites) != 1 || le32(code[4:]) != a64UDF0 {
		t.Fatalf("fast=%d word=%#x", w.Fast, le32(code[4:]))
	}
}

// TestA64Sysreg checks the tpidr and ctr islands: each site becomes `b
// island` and each island ends in a branch back past the site.
func TestA64Sysreg(t *testing.T) {
	const base, vaddr = 0x100000, 0x200000
	var code []byte
	code = put32(code, 0xd53bd041) // mrs x1, tpidr_el0
	code = put32(code, 0xd51bd050) // msr tpidr_el0, x16
	code = put32(code, 0xd53b0022) // mrs x2, ctr_el0
	code = put32(code, a64RET)
	w := NewA64(base, 1<<20, 0)
	if left := w.Sysreg([]int{0, 4, 8, 12}, code, vaddr, 0x1678, 0x8444c004); len(left) != 0 || w.Sysregs != 3 {
		t.Fatalf("left=%x sysregs=%d", left, w.Sysregs)
	}
	tr := w.Trampolines()
	want := [][]uint32{
		{0xd53bd041, 0xf94b3c21},
		{0xf81f0ff1, 0xd53bd051, 0xf90b3e30, 0xf84107f1},
		{0xd2980082, 0xf2b08882},
	}
	at := uint64(headerSize)
	for i, is := range want {
		pc := uint64(vaddr + 4*i)
		if got := le32(code[4*i:]); got != encB(pc, base+at) {
			t.Fatalf("site %d = %#x", i, got)
		}
		for k, ins := range is {
			if got := le32(tr[at+uint64(4*k):]); got != ins {
				t.Fatalf("island %d word %d = %#x, want %#x", i, k, got, ins)
			}
		}
		end := at + uint64(4*len(is))
		if got := le32(tr[end:]); got != encB(base+end, pc+4) {
			t.Fatalf("island %d branch back = %#x", i, got)
		}
		at = end + 4
	}
	if le32(code[12:]) != a64RET {
		t.Fatal("ret was patched")
	}
}

// TestA64OwnStackExit checks the OwnStack epilogue and that, with
// ExitStack, an exit site branches to an island that switches stacks before
// it jumps to the exit entry.
func TestA64OwnStackExit(t *testing.T) {
	const base, vaddr, entry, exit = 0x100000, 0x200000, 0x6f0000001000, 0x6f0000002000
	code := put32(put32(put32(nil, a64SVC0), 0xd2800ba8), a64SVC0) // svc; mov x8, #93; svc
	w := NewA64(base, 1<<20, entry)
	w.OwnStack()
	w.ExitStack(0x1660, exit)
	w.Apply([]int{0, 8}, code, vaddr)
	tr := w.Trampolines()
	island := uint64(base + headerSize)
	if le32(code[0:]) != encB(vaddr, island) || le32(tr[headerSize+28:]) != 0xa94147f0 || le32(tr[headerSize+32:]) != 0x910283ff {
		t.Fatalf("island: site %#x epilogue %#x %#x", le32(code[0:]), le32(tr[headerSize+28:]), le32(tr[headerSize+32:]))
	}
	ex := island + a64IslandSize
	if le32(code[8:]) != encB(vaddr+8, ex) {
		t.Fatalf("exit site = %#x, want b exit island", le32(code[8:]))
	}
	e := tr[ex-base:]
	want := []uint32{0xd53bd050, 0xf94b3210, 0x9100021f, encLdrLit(ex+12, ex+24, 16), 0xd61f0200}
	for i, ins := range want {
		if got := le32(e[4*i:]); got != ins {
			t.Fatalf("exit island word %d = %#x, want %#x", i, got, ins)
		}
	}
	if got := binary.LittleEndian.Uint64(e[24:]); got != exit {
		t.Fatalf("exit entry = %#x", got)
	}
}

// TestA64Replace checks a replaced function branches to an island that loads
// x8, enters the stub and returns.
func TestA64Replace(t *testing.T) {
	const base, vaddr, entry = 0x100000, 0x200000, 0x6f0000001000
	code := put32(put32(nil, 0xd503201f), 0xd65f03c0)
	w := NewA64(base, 1<<20, entry)
	if !w.Replace(code, vaddr, 0, 0x4c430030) {
		t.Fatal("Replace refused")
	}
	tr := w.Trampolines()
	at := uint64(base + headerSize)
	if le32(code) != encB(vaddr, at) {
		t.Fatalf("entry = %#x, want b island", le32(code))
	}
	i := tr[at-base:]
	if le32(i[0:]) != 0xd2800608 || le32(i[4:]) != 0xf2a98868 {
		t.Fatalf("x8 load = %#x %#x", le32(i[0:]), le32(i[4:]))
	}
	if got := le32(i[8+40:]); got != encB(at+8+40, at+72) {
		t.Fatalf("island resumes with %#x", got)
	}
	if le32(i[72:]) != a64RET || binary.LittleEndian.Uint64(i[8+56:]) != entry {
		t.Fatalf("tail %#x, entry %#x", le32(i[72:]), binary.LittleEndian.Uint64(i[8+56:]))
	}
}

// An island built for code at one address is the same bytes for the code
// and its area moved by a page multiple: guest addresses in it are
// PC-relative, so a prepared image relocates without a fixup.
func TestA64IslandRelocates(t *testing.T) {
	const entry = 0x6f0000001000
	build := func(base, vaddr uint64) ([]byte, []byte) {
		code := put32(put32(put32(nil, 0xd2800808), a64SVC0), a64RET)
		w := NewA64(base, 1<<20, entry)
		w.Apply([]int{4}, code, vaddr)
		return code, w.Trampolines()
	}
	c1, t1 := build(0x10000000, 0x10123000)
	c2, t2 := build(0x10000000+0x7654000, 0x10123000+0x7654000)
	if !reflect.DeepEqual(c1, c2) || !reflect.DeepEqual(t1, t2) {
		t.Fatalf("island moved: code % x vs % x", c1, c2)
	}
	isl := t1[headerSize:]
	want := map[int]string{
		4:  "ADR X16, .+0x18",      // the epilogue, at +28
		12: "ADRP X17, .+0x123000", // the resume address's page
		16: "ADD X17, X17, #0x8",   // and its offset in it
	}
	for off, w := range want {
		if in, err := arm64asm.Decode(isl[off:]); err != nil || in.String() != w {
			t.Errorf("+%d: %v (%v), want %s", off, in, err, w)
		}
	}
}
