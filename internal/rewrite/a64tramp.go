package rewrite

import "encoding/binary"

// A64Rewriter is the arm64 counterpart of Rewriter: it turns svc sites into
// branches to per-site islands in trampoline areas that enter the stub's
// lc_syscall_entry. Each area starts with a 16-byte header (the entry
// address), like the x86 one. A site's island goes in the first area within
// B range of it that has room: code larger than that range needs an area on
// each side.
type A64Rewriter struct {
	areas []a64Area
	entry uint64
	// NoStray skips the scan for stray svc words in Apply.
	NoStray bool
	Result
	// Sysregs counts system-register accesses moved to islands (Sysreg).
	Sysregs int
	// ownStack and exitSlot: see OwnStack and ExitStack.
	ownStack  bool
	exitSlot  uint32
	exitEntry uint64
}

type a64Area struct {
	base, limit uint64
	tramp       []byte
}

// A64Area is a trampoline area: Size bytes at Base.
type A64Area struct{ Base, Size uint64 }

const (
	a64IslandSize = 64
	a64BReach     = 1 << 27 // B's ±128MB
)

// NewA64 returns an A64Rewriter whose islands live in [base, base+size).
// A site out of B range of its island, or past a full area, is patched to
// `udf #0` instead and reaches the stub through SIGILL.
func NewA64(base, size, entry uint64) *A64Rewriter {
	return NewA64Areas([]A64Area{{base, size}}, entry)
}

// NewA64Areas returns an A64Rewriter whose islands live in the given areas.
func NewA64Areas(areas []A64Area, entry uint64) *A64Rewriter {
	w := &A64Rewriter{entry: entry}
	for _, a := range areas {
		t := make([]byte, headerSize)
		binary.LittleEndian.PutUint64(t, entry)
		w.areas = append(w.areas, a64Area{base: a.Base, limit: a.Size, tramp: t})
	}
	return w
}

// A64AreaBytes bounds the area that holds the islands of n sites.
func A64AreaBytes(n int) uint64 {
	return headerSize + uint64(n)*(a64IslandSize+12)
}

// Trampolines returns the first area's contents.
func (w *A64Rewriter) Trampolines() []byte { return w.areas[0].tramp }

// Areas returns each area's contents, in the order NewA64Areas took them.
func (w *A64Rewriter) Areas() [][]byte {
	out := make([][]byte, len(w.areas))
	for i, a := range w.areas {
		out[i] = a.tramp
	}
	return out
}

// room returns the first area with n bytes free whose next island, at at, the
// site at pc reaches with a b, and which branches back from at+back to pc+4
// (back < 0: the island does not return there). nil when there is none.
func (w *A64Rewriter) room(pc, n uint64, back int) (*a64Area, uint64) {
	for i := range w.areas {
		a := &w.areas[i]
		at := a.base + uint64(len(a.tramp))
		if uint64(len(a.tramp))+n <= a.limit && inB(pc, at) && (back < 0 || inB(at+uint64(back), pc+4)) {
			return a, at
		}
	}
	return nil, 0
}

// Apply rewrites the svc words at the given offsets of code, which is
// loaded at vaddr. An offset that does not hold `svc #0` is skipped: sites
// are a hint, never patched blindly.
func (w *A64Rewriter) Apply(sites []int, code []byte, vaddr uint64) {
	for _, off := range sites {
		if off < 0 || off%4 != 0 || off+4 > len(code) || le32(code[off:]) != a64SVC0 {
			continue
		}
		pc := vaddr + uint64(off)
		w.Sites = append(w.Sites, pc)
		if !a64LoadsExit(code, off) {
			if a, at := w.room(pc, a64IslandSize, 36); a != nil {
				w.island(a, at, pc+4)
				putLE32(code[off:], encB(pc, at))
				w.Fast++
				continue
			}
		} else if w.exitSlot != 0 {
			if a, at := w.room(pc, a64IslandSize, -1); a != nil {
				w.exitIsland(a, at)
				putLE32(code[off:], encB(pc, at))
				continue
			}
		}
		putLE32(code[off:], a64UDF0)
	}
	for i := 0; !w.NoStray && i+4 <= len(code); i += 4 {
		if le32(code[i:]) == a64SVC0 {
			w.Stray = append(w.Stray, vaddr+uint64(i))
		}
	}
}

// OwnStack is for a stub that moves to a stack of its own at once and
// returns with sp where the island left it: the epilogue reloads the guest's
// x16/x17 from the island's own save, so nothing is written below the
// guest's stack pointer but the island's 160 bytes.
func (w *A64Rewriter) OwnStack() { w.ownStack = true }

// ExitStack gives exit sites an island that switches to the stack whose top
// the stub keeps at [tpidr_el0 + slot] and jumps to entry with the guest's
// x0, without touching the guest's stack: a host whose exceptions are
// dispatched on the faulting stack (Windows) cannot serve the trap there.
func (w *A64Rewriter) ExitStack(slot uint32, entry uint64) {
	w.exitSlot = slot
	w.exitEntry = entry
}

// exitIsland emits an exit site's island at the end of the area:
//
//	mrs x16, tpidr_el0
//	ldr x16, [x16, #slot]
//	mov sp, x16
//	ldr x16, Lexit
//	br  x16
//	nop
//	.quad entry
func (w *A64Rewriter) exitIsland(a *a64Area, at uint64) {
	t := a.tramp
	t = binary.LittleEndian.AppendUint32(t, a64MrsTpidr|16)
	t = binary.LittleEndian.AppendUint32(t, 0xf9400210|w.exitSlot/8<<10)
	t = binary.LittleEndian.AppendUint32(t, 0x9100021f)
	t = binary.LittleEndian.AppendUint32(t, encLdrLit(at+12, at+24, 16))
	t = binary.LittleEndian.AppendUint32(t, 0xd61f0200)
	t = binary.LittleEndian.AppendUint32(t, a64NOP)
	t = binary.LittleEndian.AppendUint64(t, w.exitEntry)
	a.tramp = t
}

// Replace turns the function at off of code (loaded at vaddr) into a call to
// the stub: its first instruction becomes a branch to an island that loads
// nr into x8, enters the stub as a svc site's island does, and returns to the
// caller. The stub serves nr with the function's arguments in x0..x5; x0
// comes back as the result, and x8 is clobbered (both are caller-saved, so
// any function may be replaced this way). False when off does not hold an
// instruction or the area is full or out of reach.
//
//	 0: movz x8, #nr
//	 4: movk x8, #nr>>16, lsl #16
//	 8: <a svc site's island, resuming at 72>
//	72: ret
func (w *A64Rewriter) Replace(code []byte, vaddr uint64, off int, nr uint32) bool {
	if off < 0 || off%4 != 0 || off+4 > len(code) {
		return false
	}
	pc := vaddr + uint64(off)
	a, at := w.room(pc, a64IslandSize+12, -1)
	if a == nil {
		return false
	}
	a.tramp = binary.LittleEndian.AppendUint32(a.tramp, 0xd2800008|(nr&0xffff)<<5)
	a.tramp = binary.LittleEndian.AppendUint32(a.tramp, 0xf2a00008|(nr>>16)<<5)
	w.island(a, at+8, at+8+a64IslandSize)
	a.tramp = binary.LittleEndian.AppendUint32(a.tramp, a64RET)
	putLE32(code[off:], encB(pc, at))
	return true
}

// a64LoadsExit reports whether the svc at off follows `mov x8|w8, #93`
// (exit). A thread's exit keeps the trap, which the stub serves on its own
// stack: the guest's may be free already (Go's exitThread says so before
// the syscall), and an island writes below the guest's stack pointer.
func a64LoadsExit(code []byte, off int) bool {
	if off < 4 {
		return false
	}
	w := le32(code[off-4:])
	return w == 0xd2800ba8 || w == 0x52800ba8
}

// island emits, at address at, the entry for one site and the per-site
// epilogue the stub returns through. Only a direct `b` leads back, so no
// guest register is sacrificed: the Linux ABI keeps every register but x0
// across svc, including x16/x17, which an inlined syscall's caller may keep
// live. Nor is memory below sp touched: Linux leaves it alone across svc and
// code relies on that (Go's runtime.clone keeps values at -24..-8(sp) over a
// gettid), so the island first skips a 128-byte gap, as the x86 trampolines
// skip the red zone. The stub's frame is 272 bytes (see lc_syscall_entry).
//
//	 0: stp x16, x17, [sp, #-144]!  skip the gap, save guest x16/x17
//	 4: adr x16, epilogue
//	 8: str x16, [sp, #-16]!        push &epilogue
//	12: adrp x17, resume            the stub records it as guest_rip
//	16: add x17, x17, :lo12:resume
//	20: ldr x16, Lentry
//	24: br  x16
//	28: ldr x17, [sp, #136]         epilogue: guest x17 from the frame
//	32: ldr x16, [sp, #128]         guest x16
//	36: add sp, sp, #432            pop the frame, both pushes and the gap
//	40: b   resume
//	44: nop; nop; nop
//	56: .quad entry
//
// With OwnStack the epilogue is `ldp x16, x17, [sp, #16]; add sp, sp, #160;
// nop`: the stub returns with sp at the island's push. Guest addresses are
// all PC-relative, so the island moves with the code it serves by any page
// multiple; only the stub's entry is absolute.
func (w *A64Rewriter) island(a *a64Area, at, resume uint64) {
	t := a.tramp
	t = binary.LittleEndian.AppendUint32(t, 0xa9b747f0)
	t = binary.LittleEndian.AppendUint32(t, encADR(at+4, at+28, 16))
	t = binary.LittleEndian.AppendUint32(t, 0xf81f0ff0)
	t = binary.LittleEndian.AppendUint32(t, encADRP(at+12, resume, 17))
	t = binary.LittleEndian.AppendUint32(t, 0x91000000|uint32(resume&0xfff)<<10|17<<5|17) // add x17, x17, #lo12
	t = binary.LittleEndian.AppendUint32(t, encLdrLit(at+20, at+56, 16))
	t = binary.LittleEndian.AppendUint32(t, 0xd61f0200)
	if w.ownStack {
		t = binary.LittleEndian.AppendUint32(t, 0xa94147f0)
		t = binary.LittleEndian.AppendUint32(t, 0x910283ff)
		t = binary.LittleEndian.AppendUint32(t, a64NOP)
	} else {
		t = binary.LittleEndian.AppendUint32(t, 0xf94047f1)
		t = binary.LittleEndian.AppendUint32(t, 0xf94043f0)
		t = binary.LittleEndian.AppendUint32(t, 0x9106c3ff)
	}
	t = binary.LittleEndian.AppendUint32(t, encB(at+40, resume))
	t = binary.LittleEndian.AppendUint32(t, a64NOP)
	t = binary.LittleEndian.AppendUint32(t, a64NOP)
	t = binary.LittleEndian.AppendUint32(t, a64NOP)
	t = binary.LittleEndian.AppendUint64(t, w.entry)
	a.tramp = t
}

func inB(from, to uint64) bool {
	d := int64(to - from)
	return d >= -a64BReach && d < a64BReach
}

func encB(from, to uint64) uint32 {
	return 0x14000000 | uint32((to-from)>>2)&0x03ffffff
}

// encADR is `adr xRd, to` at from (within +-1 MiB).
func encADR(from, to uint64, rd uint32) uint32 {
	d := uint32(to - from)
	return 0x10000000 | (d&3)<<29 | (d>>2&0x7ffff)<<5 | rd
}

// encADRP is `adrp xRd, to` at from: to's 4 KiB page, relative to from's.
func encADRP(from, to uint64, rd uint32) uint32 {
	d := uint32((to>>12 - from>>12) & 0x1fffff)
	return 0x90000000 | (d&3)<<29 | (d>>2)<<5 | rd
}

func encLdrLit(from, lit uint64, rt uint32) uint32 {
	return 0x58000000 | (uint32((lit-from)>>2)&0x7ffff)<<5 | rt
}

// Sysreg turns the system-register sites at the given offsets of code (see
// IsA64Sysreg) into branches to islands, for a host that keeps its own pointer
// in tpidr_el0 (Windows arm64: the TEB). The guest's thread pointer lives at
// [tpidr_el0 + tpOff] instead:
//
//	mrs xN, tpidr_el0  ->  mrs xN, tpidr_el0; ldr xN, [xN, #tpOff]; b back
//	msr tpidr_el0, xN  ->  str xS, [sp, #-16]!; mrs xS, tpidr_el0
//	                       str xN, [xS, #tpOff]; ldr xS, [sp], #16; b back
//	mrs xN, ctr_el0    ->  movz xN, #ctr; movk xN, #ctr>>16, lsl 16; b back
//
// xS is x16, or x17 when xN is x16. Offsets that hold no such access are
// skipped. A site its island cannot reach stays as it is, and is returned:
// the caller must refuse the image, since a tpidr access left in place reads
// the host's pointer.
func (w *A64Rewriter) Sysreg(offs []int, code []byte, vaddr uint64, tpOff uint32, ctr uint32) []uint64 {
	var left []uint64
	for _, off := range offs {
		if off < 0 || off%4 != 0 || off+4 > len(code) {
			continue
		}
		ins := le32(code[off:])
		if !IsA64Sysreg(ins) {
			continue
		}
		pc := vaddr + uint64(off)
		a, at := w.room(pc, a64SysregSize, a64SysregSize)
		if a == nil {
			left = append(left, pc)
			continue
		}
		n := ins & 0x1f
		var is []uint32
		switch ins &^ 0x1f {
		case a64MrsTpidr:
			is = []uint32{a64NOP, a64NOP}
			if n != 31 { // as a base register, 31 is sp
				is = []uint32{a64MrsTpidr | n, 0xf9400000 | tpOff/8<<10 | n<<5 | n}
			}
		case a64MsrTpidr:
			s := uint32(16)
			if n == 16 {
				s = 17
			}
			is = []uint32{0xf81f0fe0 | s, a64MrsTpidr | s, 0xf9000000 | tpOff/8<<10 | s<<5 | n, 0xf84107e0 | s}
		case a64MrsCtr:
			is = []uint32{0xd2800000 | (ctr&0xffff)<<5 | n, 0xf2a00000 | (ctr>>16)<<5 | n}
		}
		t := a.tramp
		for _, i := range is {
			t = binary.LittleEndian.AppendUint32(t, i)
		}
		t = binary.LittleEndian.AppendUint32(t, encB(at+uint64(4*len(is)), pc+4))
		a.tramp = t
		putLE32(code[off:], encB(pc, at))
		w.Sysregs++
	}
	return left
}

const (
	a64SysregSize = 20
	a64NOP        = 0xd503201f
)
