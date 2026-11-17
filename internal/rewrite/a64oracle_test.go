package rewrite

import (
	"testing"

	"golang.org/x/arch/arm64/arm64asm"
)

// The disassembler-based flow analysis, kept only as a test oracle: the
// production descent uses the bitmask classifier (a64flowFast), and the
// differential test (a64diff_test.go) checks the classifier against this.

// a64flowDecode is the flow step via the full disassembler: ok is false for an
// undefined encoding, which stops that path (as a literal pool would).
func a64flowDecode(w uint32, off int) (target int, takeTarget, fallThrough, ok bool) {
	var b [4]byte
	putLE32(b[:], w)
	inst, err := arm64asm.Decode(b[:])
	if err != nil {
		return 0, false, false, false
	}
	t, take, fall := a64flow(inst, off)
	return t, take, fall, true
}

// a64flow classifies an instruction's control flow: its direct branch target
// (if any), whether that target is taken, and whether execution falls through.
func a64flow(inst arm64asm.Inst, off int) (target int, takeTarget, fallThrough bool) {
	rel, hasRel := a64pcrel(inst)
	if hasRel {
		target = off + rel
	}
	switch inst.Op {
	case arm64asm.B:
		if a64hasCond(inst) {
			return target, hasRel, true // conditional: branch or fall through
		}
		return target, hasRel, false // unconditional: branch only
	case arm64asm.CBZ, arm64asm.CBNZ, arm64asm.TBZ, arm64asm.TBNZ:
		return target, hasRel, true // conditional
	case arm64asm.BL:
		return target, hasRel, true // call: target runs, then returns here
	case arm64asm.RET, arm64asm.BR, arm64asm.ERET:
		return 0, false, false // no further direct flow
	case arm64asm.BLR:
		return 0, false, true // indirect call returns to the next instruction
	case arm64asm.BRK, arm64asm.HLT, arm64asm.HVC, arm64asm.SMC:
		return 0, false, false // traps: do not assume fall-through
	default:
		return 0, false, true // a PC-relative operand here (ADR, a literal load) is not flow
	}
}

func a64pcrel(inst arm64asm.Inst) (int, bool) {
	for _, a := range inst.Args {
		if p, ok := a.(arm64asm.PCRel); ok {
			return int(p), true
		}
	}
	return 0, false
}

func a64hasCond(inst arm64asm.Inst) bool {
	for _, a := range inst.Args {
		if _, ok := a.(arm64asm.Cond); ok {
			return true
		}
	}
	return false
}

// For every valid arm64 instruction, the bitmask classifier's control-flow
// classification must match the disassembler's exactly.
func FuzzA64FlowFast(f *testing.F) {
	for _, w := range []uint32{
		0x14000002, 0x94000002, 0x54000042, 0x34000040, 0xb4000040, 0x36000040,
		0xd65f03c0, 0xd61f0000, 0xd63f0000, 0xd69f03e0, 0xd4200000, 0xd503201f,
		0x10000000, 0x90000000, 0x58000040, 0x18000040, 0xd2801068,
	} {
		f.Add(w, 0x1000)
	}
	f.Fuzz(func(t *testing.T, w uint32, off int) {
		if off < 0 {
			off = -off
		}
		off &^= 3
		var b [4]byte
		putLE32(b[:], w)
		inst, err := arm64asm.Decode(b[:])
		if err != nil {
			return // undefined encoding: the descent handles these separately
		}
		wt, wtake, wfall := a64flowFast(w, off)
		rt, rtake, rfall := a64flow(inst, off)
		if wtake != rtake || wfall != rfall || (rtake && wt != rt) {
			t.Fatalf("%#08x (%s) off=%d: fast=(t=%d take=%v fall=%v) ref=(t=%d take=%v fall=%v)",
				w, inst, off, wt, wtake, wfall, rt, rtake, rfall)
		}
	})
}
