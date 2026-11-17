package rewrite

import "slices"

// arm64 rewriting, shared by the macOS backend and the Linux arm64 backend.
// Every A64 instruction is a 4-byte aligned word, and a Linux syscall is
// `svc #0` (0xd4000001). Each site becomes a branch to a per-site island that
// enters the stub (built by the macOS stub itself, or by A64Rewriter on
// Linux), with `udf #0` (0x00000000, a trap) as the in-place fallback.
//
// Finding the svc sites safely means not misreading data embedded in the text
// (arm64 literal pools) as instructions. We use the same recursive-descent
// idea as the x86 path: decode only from reachable entry points, following
// direct branches, so a data word is never decoded as a syscall. A syscall
// reachable only through an indirect branch we don't model is missed, and that
// is contained by the stub's backstop (seccomp on Linux, Seatbelt on macOS)
// exactly as an unrewritten x86 syscall is contained by seccomp.

const (
	a64SVC0 = 0xd4000001 // svc #0
	a64UDF0 = 0x00000000 // udf #0
	a64RET  = 0xd65f03c0 // ret

	// System-register accesses the stub rewrites (low 5 bits = register):
	//   mrs xN, tpidr_el0 (0xd53bd040) / msr tpidr_el0, xN (0xd51bd040): musl's
	//     thread pointer, which Darwin clobbers on every context switch.
	//   mrs xN, ctr_el0 (0xd53b0020): the Cache Type Register, which Apple
	//     Silicon traps at EL0 (SIGILL); musl's __clear_cache reads it for JIT.
	// See A64SysregSites.
	a64MrsTpidr = 0xd53bd040
	a64MsrTpidr = 0xd51bd040
	a64MrsCtr   = 0xd53b0020
)

// A64Sites returns the byte offsets of every `svc #0` reachable by descent
// from entries (offsets into code). code is the executable bytes at some base;
// offsets are relative to its start.
func A64Sites(code []byte, entries []int) []int {
	a64Mu.Lock()
	defer a64Mu.Unlock()
	return a64scan(code, entries, isSVC0, a64flowFastOK)
}

// a64flowFastOK is the recursive descent's flow step via the bitmask classifier;
// ok is always true (an unrecognized word just falls through).
func a64flowFastOK(w uint32, off int) (target int, takeTarget, fallThrough, ok bool) {
	t, take, fall := a64flowFast(w, off)
	return t, take, fall, true
}

// IsA64Sysreg reports whether w is a system-register access the stub rewrites:
// mrs/msr tpidr_el0, or mrs ctr_el0 (low 5 bits = the register). Unlike a svc
// (0xd4000001, which collides with common data words), these encodings are
// distinctive enough to find by a linear scan of executable code, so every one
// is caught -- including those behind indirect branches that recursive descent
// cannot reach (V8 is full of them). See sandbox.computeSites.
func IsA64Sysreg(w uint32) bool {
	switch w & 0xffffffe0 {
	case a64MrsTpidr, a64MsrTpidr, a64MrsCtr:
		return true
	}
	return false
}

// a64scan recursive-descends from entries, following direct control flow, and
// returns the offsets of every instruction match reports as interesting (svc,
// or tpidr access). A matched instruction falls through (none of them branch),
// so descent continues past it; decoding only reachable code keeps a data word
// in a literal pool from being mistaken for an instruction. It also records
// the words the code it decodes loads as data (literal loads), its own
// declaration that they are not instructions.
// Scan scratch, reused across calls (every a64scan caller holds a64Mu): the
// visited and data bitmaps and the work queue, so a scan allocates only its
// result. The bitmaps hold the last scan's until the next.
var (
	a64SeenBuf  []bool
	a64DataBuf  []bool
	a64QueueBuf []int
	a64Exits    []int // the last scan's direct branch targets outside code
)

func a64scan(code []byte, entries []int, match func(uint32) bool, flow a64flowFn) []int {
	n := len(code) &^ 3
	nn := n/4 + 1
	if cap(a64SeenBuf) < nn {
		a64SeenBuf = make([]bool, nn)
	}
	seen := a64SeenBuf[:nn]
	clear(seen)
	if cap(a64DataBuf) < nn {
		a64DataBuf = make([]bool, nn)
	}
	data := a64DataBuf[:nn]
	clear(data)
	var found []int
	queue := a64QueueBuf[:0]
	defer func() { a64QueueBuf = queue[:0] }() // keep the grown capacity
	a64Exits = a64Exits[:0]
	push := func(off int) {
		if off < 0 || off >= n {
			a64Exits = append(a64Exits, off)
		} else if off%4 == 0 && !seen[off/4] {
			queue = append(queue, off)
		}
	}
	for _, e := range entries {
		push(e)
	}
	if len(entries) == 0 {
		push(0)
	}
	for len(queue) > 0 {
		off := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[off/4] {
			continue
		}
		seen[off/4] = true

		word := le32(code[off:])
		if t, size := a64Literal(word, off); size > 0 {
			for b := t &^ 3; b < t+size && b+4 <= n; b += 4 {
				if b >= 0 {
					data[b/4] = true
				}
			}
		}
		if match(word) {
			found = append(found, off)
			push(off + 4) // svc/mrs/msr all fall through
			continue
		}
		if word == a64SVC0 {
			push(off + 4) // a syscall falls through (even when scanning for tpidr)
			continue
		}
		tgt, takeTarget, fallThrough, ok := flow(word, off)
		if !ok {
			continue // not reachable code (likely a literal pool); stop this path
		}
		if takeTarget {
			push(tgt)
		}
		if fallThrough {
			push(off + 4)
		}
	}
	slices.Sort(found) // the queue is LIFO
	return found
}

// a64flowFn is a recursive-descent flow step (see a64flowFast/a64flowDecode).
type a64flowFn func(w uint32, off int) (target int, takeTarget, fallThrough, ok bool)

// a64flowFast classifies an instruction's control flow directly from its 32-bit
// encoding, without the disassembler. A64 branch/return encodings are fixed bit
// patterns, so the recursive descent needs no full decode (which boxes every
// operand into an interface and dominates both time and allocations). Unlike
// the arm64asm path it does not chase ADR/ADRP/literal-load PC-relative operands
// as control flow: those name data, never a branch destination, so following
// them only risks reading a literal pool as code. Flags: whether the direct
// target is taken, and whether execution falls through.
func a64flowFast(w uint32, off int) (target int, takeTarget, fallThrough bool) {
	switch {
	case w&0xfc000000 == 0x14000000: // B (unconditional)
		return off + imm26(w), true, false
	case w&0xfc000000 == 0x94000000: // BL (call: target runs, returns here)
		return off + imm26(w), true, true
	case w&0xff000010 == 0x54000000: // B.cond
		return off + imm19(w), true, true
	case w&0x7e000000 == 0x34000000: // CBZ/CBNZ
		return off + imm19(w), true, true
	case w&0x7e000000 == 0x36000000: // TBZ/TBNZ
		return off + imm14(w), true, true
	case w&0xfffffc1f == 0xd61f0000: // BR (indirect: no direct flow)
		return 0, false, false
	case w&0xfffffc1f == 0xd63f0000: // BLR (indirect call: returns to next)
		return 0, false, true
	case w&0xfffffc1f == 0xd65f0000: // RET
		return 0, false, false
	case w == 0xd69f03e0: // ERET
		return 0, false, false
	case w&0xffe0001f == 0xd4200000, // BRK
		w&0xffe0001f == 0xd4400000, // HLT
		w&0xffe0001f == 0xd4000002, // HVC
		w&0xffe0001f == 0xd4000003: // SMC
		return 0, false, false // traps: do not assume fall-through
	default:
		// Anything else falls through. A PC-relative operand is not flow: a
		// literal load's names data, an ADRP's only a page; the code an ADR or
		// ADRP+ADD names is an entry of its own (addrTaken).
		return 0, false, true
	}
}

// a64Literal returns, for a literal load (LDR, LDRSW, PRFM or a SIMD LDR from
// a PC-relative address) at off, the offset and size of what it reads; size 0
// for any other word.
func a64Literal(w uint32, off int) (int, int) {
	if w&0x3b000000 != 0x18000000 {
		return 0, 0
	}
	opc := w >> 30
	size := 4 << opc // GPR: w, x, ldrsw (4), prfm; SIMD: s, d, q
	if w&(1<<26) == 0 && opc >= 2 {
		size = 4
	}
	return off + imm19(w), size
}

// adrImm assembles ADR/ADRP's 21-bit immhi:immlo.
func adrImm(w uint32) uint32 { return (w>>5)&0x7ffff<<2 | (w>>29)&0x3 }

func sext21(v uint32) int32 { return int32(v<<11) >> 11 }

// imm26/imm19/imm14 decode a branch's sign-extended, word-scaled displacement
// (bytes) from its encoding.
func imm26(w uint32) int { return int(int32(w<<6)>>6) << 2 }   // bits[25:0]
func imm19(w uint32) int { return int(int32(w<<8)>>13) << 2 }  // bits[23:5]
func imm14(w uint32) int { return int(int32(w<<13)>>18) << 2 } // bits[18:5]

// A64Patch rewrites the given svc sites to `udf #0` in place.
func A64Patch(code []byte, sites []int) {
	for _, off := range sites {
		putLE32(code[off:], a64UDF0)
	}
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func le64(b []byte) uint64 {
	return uint64(le32(b)) | uint64(le32(b[4:]))<<32
}

func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
