package rewrite

import (
	"testing"

	"golang.org/x/arch/x86/x86asm"
)

// x86decode must agree with the disassembler, for length and control flow, at
// every offset of real amd64 code where the disassembler decodes an instruction.
func TestX86DecodeMatchesDisasm(t *testing.T) {
	code := realX86Text(t)
	var checked, mism int
	for off := 0; off < len(code); off++ {
		ref, err := x86asm.Decode(code[off:], 64)
		if err != nil || ref.Len == 0 || ref.Op == 0 {
			continue
		}
		if endbrLen(code[off:]) > 0 {
			continue // handled specially by the descent, not the decoder
		}
		got := x86decode(code[off:])
		if !got.ok {
			continue // a conservative give-up stops the descent safely
		}
		checked++
		bad := ""
		if got.len != ref.Len {
			bad = "len"
		} else if got.syscall != (ref.Op == x86asm.SYSCALL) {
			bad = "syscall"
		} else if got.terminates != terminates(ref.Op) {
			bad = "terminates"
		} else if got.movable && !movable(ref) {
			// Only the unsafe direction is a bug: claiming an instruction can be
			// stolen into a trampoline when the oracle says it cannot. The
			// decoder is intentionally stricter for VEX/EVEX RIP-relative forms
			// (it marks them non-movable; the disassembler does not), which is
			// the safe direction and allowed.
			bad = "movable (claimed movable, oracle says not)"
		} else if got.fsMem != (fsMem(ref) != nil) {
			bad = "fsMem"
		} else {
			rt, rok := branchTarget(ref, off)
			if got.relOK != rok || (rok && off+got.len+got.rel != rt) {
				bad = "branch"
			}
		}
		if bad != "" {
			mism++
			if mism <= 20 {
				t.Errorf("off %d % x: %s — got %+v ref=%s(len=%d term=%v mov=%v fs=%v)",
					off, code[off:off+min(ref.Len, 12)], bad, got, ref.Op, ref.Len,
					terminates(ref.Op), movable(ref), fsMem(ref) != nil)
			}
		}
	}
	t.Logf("checked %d offsets, %d mismatches", checked, mism)
}

// Fuzz the decoder against the disassembler across the encoding space.
func FuzzX86Decode(f *testing.F) {
	for _, s := range [][]byte{
		{0x0f, 0x05}, {0xc3}, {0xe9, 1, 2, 3, 4}, {0x74, 5}, {0xff, 0x25, 0, 0, 0, 0},
		{0x48, 0x89, 0xde}, {0x64, 0xa0, 1, 2, 3, 4, 5, 6, 7, 8}, {0xf6, 0xc2, 1},
		{0x0f, 0x1f, 0x44, 0, 0}, {0xcf}, {0xf3, 0x90}, {0x62, 0xf1, 0xfe, 0x48, 0x6f, 0x05, 1, 2, 3, 4},
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if endbrLen(b) > 0 {
			return
		}
		ref, err := x86asm.Decode(b, 64)
		if err != nil || ref.Len == 0 || ref.Op == 0 {
			return
		}
		got := x86decode(b)
		if !got.ok {
			return // a conservative give-up stops the descent safely
		}
		// Length is the desync-critical invariant across the encoding space: a
		// wrong length when we claim ok mis-frames every following instruction.
		// (syscall and branch share the same consequence — a missed syscall or a
		// bogus descent target.) The remaining flow fields are validated exactly
		// on real code by TestX86DecodeMatchesDisasm; on malformed fuzz bytes the
		// disassembler has its own lenient quirks (double segment prefixes, test
		// registers) that are not a useful oracle.
		if got.len != ref.Len {
			t.Fatalf("% x: len got=%d ref=%d(%s)", b[:min(len(b), 15)], got.len, ref.Len, ref.Op)
		}
		if got.syscall != (ref.Op == x86asm.SYSCALL) {
			t.Fatalf("% x (%s): syscall got=%v", b[:min(len(b), 15)], ref.Op, got.syscall)
		}
		if rt, rok := branchTarget(ref, 0); got.relOK != rok || (rok && got.len+got.rel != rt) {
			t.Fatalf("% x (%s): branch got rel=%d ok=%v ref target=%d ok=%v", b[:min(len(b), 15)], ref.Op, got.rel, got.relOK, rt, rok)
		}
	})
}

// RipDisp finds a RIP-relative operand's displacement, legacy and VEX, and
// none where the operand is not RIP-relative.
func TestRipDisp(t *testing.T) {
	for _, c := range []struct {
		code  []byte
		n, at int
	}{
		{[]byte{0x48, 0x89, 0x3d, 0x10, 0, 0, 0}, 7, 3},          // mov [rip+0x10], rdi
		{[]byte{0xf0, 0x48, 0x0f, 0xc1, 0x05, 8, 0, 0, 0}, 9, 5}, // lock xadd [rip+8], rax
		{[]byte{0xc7, 0x05, 4, 0, 0, 0, 0x2a, 0, 0, 0}, 10, 2},   // mov dword [rip+4], 42
		{[]byte{0xc5, 0xf9, 0x7f, 0x05, 0x20, 0, 0, 0}, 8, 4},    // vmovdqa [rip+0x20], xmm0
		{[]byte{0x89, 0x07}, 2, 0},                               // mov [rdi], eax
		{[]byte{0x48, 0x89, 0x44, 0x24, 0x08}, 5, 0},             // mov [rsp+8], rax
	} {
		if n, at := RipDisp(c.code); n != c.n || at != c.at {
			t.Errorf("% x: got (%d, %d), want (%d, %d)", c.code, n, at, c.n, c.at)
		}
	}
}
