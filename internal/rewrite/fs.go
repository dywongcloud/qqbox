package rewrite

import (
	"encoding/binary"
)

// Hosts whose FS base cannot hold the guest thread pointer (Windows resets it
// to 0 on every reschedule) keep it in a per-thread slot at a fixed %gs
// offset instead, and every %fs-relative guest instruction is rewritten to an
// island that computes the address from that slot. An instruction too short
// for the 5-byte jmp (or in a form the island cannot express) is left as is:
// it faults at a low address and the stub emulates it.

// fsIslandMax bounds an island: the spill and reload of a scratch register,
// the thread-pointer load, the re-encoded instruction and the jump back.
const fsIslandMax = 64

// FSSite is an instruction with an %fs segment override.
type FSSite struct {
	Off uint32
	Len uint8
}

// AnalyzeFS finds %fs-relative instructions by a linear sweep.
func AnalyzeFS(code []byte) []FSSite {
	var sites []FSSite
	for off := 0; off < len(code); {
		if n := endbrLen(code[off:]); n > 0 {
			off += n
			continue
		}
		in := x86decode(code[off:])
		if !in.ok {
			off++
			continue
		}
		if in.fsMem {
			sites = append(sites, FSSite{Off: uint32(off), Len: uint8(in.len)})
		}
		off += in.len
	}
	return sites
}

// FS memoizes AnalyzeFS by code content.
func (c *Cache) FS(code []byte) []FSSite {
	if len(code) >= 1<<28 { // the packed encoding holds a 28-bit offset
		return AnalyzeFS(code)
	}
	offs, _ := c.Offsets("fs-sites-1", code, func() ([]uint32, error) {
		var o []uint32
		for _, s := range AnalyzeFS(code) {
			o = append(o, s.Off<<4|uint32(s.Len))
		}
		return o, nil
	})
	sites := make([]FSSite, len(offs))
	for i, o := range offs {
		sites[i] = FSSite{Off: o >> 4, Len: uint8(o & 15)}
	}
	return sites
}

// is8bitRegOp reports whether a one-byte opcode's ModRM.reg field is an 8-bit
// GPR operand (so reg 4..7 with no REX would be AH..BH). The 0F-prefixed forms
// that reach here have 16/32/64-bit reg operands, never high bytes.
func is8bitRegOp(opcode []byte) bool {
	if len(opcode) != 1 {
		return false
	}
	switch opcode[0] {
	case 0x00, 0x02, 0x08, 0x0a, 0x10, 0x12, 0x18, 0x1a,
		0x20, 0x22, 0x28, 0x2a, 0x30, 0x32, 0x38, 0x3a, // ALU r8,rm8 / rm8,r8
		0x84, 0x86, 0x88, 0x8a: // TEST / XCHG / MOV, r8 forms
		return true
	}
	return false
}

// SegmentSlot makes the rewriter target a host whose guest thread pointer
// lives at %gs:tp rather than in the FS base: syscall trampolines jump to the
// entry with the resume address in rcx, and an instruction stolen into a
// trampoline is never %fs-relative.
func (w *Rewriter) SegmentSlot(tp uint32) {
	w.tpSlot = tp
}

// PatchFS rewrites the %fs-relative instructions in code (loaded at vaddr) to
// jumps into islands that read the thread pointer from %gs:tp. It returns the
// sites left for the fault handler.
func (w *Rewriter) PatchFS(code []byte, vaddr uint64, sites []FSSite) (left []uint64) {
	for _, s := range sites {
		off, n := int(s.Off), int(s.Len)
		if off+n > len(code) || n < 5 || !w.fsIsland(code[off:off+n], vaddr+uint64(off)) {
			left = append(left, vaddr+uint64(off))
			continue
		}
		w.FS++
	}
	return left
}

// enc is an instruction split around its memory operand.
type enc struct {
	prefixes []byte // legacy prefixes minus the %fs override
	rex      byte   // 0 if none
	opcode   []byte
	reg      byte // ModRM.reg (3 bits)
	index    int  // -1 if none
	scale    byte // SIB scale bits
	base     int  // -1 if none
	disp     int32
	tail     []byte // immediate bytes
}

func splitEnc(b []byte) (enc, bool) {
	var e enc
	i := 0
	for ; i < len(b); i++ {
		switch b[i] {
		case 0x64:
			continue
		case 0x66, 0xf2, 0xf3, 0xf0, 0x2e, 0x36, 0x3e, 0x26:
			e.prefixes = append(e.prefixes, b[i])
			continue
		case 0x65, 0x67:
			return e, false
		}
		break
	}
	if i < len(b) && b[i]&0xf0 == 0x40 {
		e.rex = b[i]
		i++
	}
	if i >= len(b) {
		return e, false
	}
	switch op := b[i]; {
	case op == 0xc4 || op == 0xc5 || op == 0x62 || op >= 0xa0 && op <= 0xa3:
		return e, false // VEX/EVEX, moffs
	case op == 0x0f && i+1 < len(b) && (b[i+1] == 0x38 || b[i+1] == 0x3a):
		e.opcode = b[i : i+3]
	case op == 0x0f:
		if i+1 < len(b) && b[i+1] == 0x0f {
			return e, false // 3DNow!
		}
		e.opcode = b[i : i+2]
	default:
		e.opcode = b[i : i+1]
	}
	i += len(e.opcode)
	if i >= len(b) {
		return e, false
	}
	modrm := b[i]
	i++
	mod, rm := modrm>>6, int(modrm&7)
	e.reg = modrm >> 3 & 7
	e.base, e.index = -1, -1
	rexB, rexX := int(e.rex&1)<<3, int(e.rex&2)<<2
	dispLen := 0
	switch mod {
	case 3:
		return e, false
	case 1:
		dispLen = 1
	case 2:
		dispLen = 4
	}
	if rm == 4 {
		if i >= len(b) {
			return e, false
		}
		sib := b[i]
		i++
		e.scale = sib >> 6
		if idx := int(sib>>3&7) | rexX; idx != 4 {
			e.index = idx
		}
		if base := int(sib & 7); mod == 0 && base == 5 {
			dispLen = 4
		} else {
			e.base = base | rexB
		}
	} else if mod == 0 && rm == 5 {
		return e, false // rip-relative
	} else {
		e.base = rm | rexB
	}
	if i+dispLen > len(b) {
		return e, false
	}
	switch dispLen {
	case 1:
		e.disp = int32(int8(b[i]))
	case 4:
		e.disp = int32(binary.LittleEndian.Uint32(b[i:]))
	}
	e.tail = b[i+dispLen:]
	return e, true
}

// rebase encodes the instruction with its memory operand as
// [r + index*scale + disp] and no segment override.
func (e enc) rebase(r int) []byte {
	out := append([]byte{}, e.prefixes...)
	rex := e.rex&0x0c | 0x40 // keep W and R
	if e.index >= 8 {
		rex |= 2
	}
	if r >= 8 {
		rex |= 1
	}
	if rex != 0x40 || e.rex != 0 {
		out = append(out, rex)
	}
	out = append(out, e.opcode...)
	mod := byte(0x80)
	if e.disp == int32(int8(e.disp)) {
		mod = 0x40
	}
	if e.index >= 0 || r&7 == 4 {
		idx := 4
		if e.index >= 0 {
			idx = e.index & 7
		}
		out = append(out, mod|e.reg<<3|4, e.scale<<6|byte(idx)<<3|byte(r&7))
	} else {
		out = append(out, mod|e.reg<<3|byte(r&7))
	}
	if mod == 0x40 {
		out = append(out, byte(e.disp))
	} else {
		out = binary.LittleEndian.AppendUint32(out, uint32(e.disp))
	}
	return append(out, e.tail...)
}

const regRSP = 4

// fsIsland emits an island for the %fs instruction b at addr and patches b to
// jump to it.
func (w *Rewriter) fsIsland(b []byte, addr uint64) bool {
	in := x86decode(b)
	if !in.ok || in.len != len(b) {
		return false
	}
	e, ok := splitEnc(b)
	if !ok || e.base == regRSP {
		return false
	}
	// push/pop/call/jmp through %fs memory touch rsp or redirect control flow,
	// so the island cannot host them (POP is 8f /0; FF /2,/3 call, /4,/5 jmp,
	// /6 push). Their rip-relative and direct forms carry no %fs operand.
	if len(e.opcode) == 1 {
		if e.opcode[0] == 0x8f || (e.opcode[0] == 0xff && e.reg >= 2 && e.reg <= 6) {
			return false
		}
	}
	rexR := 0
	if e.rex&0x04 != 0 {
		rexR = 8
	}
	regOp := int(e.reg) | rexR // the ModRM.reg operand
	// A high-byte register operand (AH..BH, reg 4..7 with no REX) would become
	// SPL..DIL if the island re-encodes the instruction with a REX prefix.
	if e.rex == 0 && e.reg >= 4 && is8bitRegOp(e.opcode) {
		return false
	}
	// The registers this instruction uses: the reg operand and the memory
	// operand's base/index (an %fs instruction has no other register operand).
	used := map[int]bool{regRSP: true, regOp: true}
	used[e.base], used[e.index] = true, true
	back := addr + uint64(len(b))
	var t []byte
	dst, isLoad := -1, false
	has66 := false
	for _, p := range e.prefixes {
		if p == 0x66 {
			has66 = true
		}
	}
	if len(e.opcode) == 1 && e.opcode[0] == 0x8b && !has66 {
		// mov r32/r64, %fs:[disp]: a 32/64-bit load with no base or index.
		dst = regOp
		isLoad = dst != regRSP && e.base < 0 && e.index < 0
	}
	if isLoad {
		// mov dst, gs:[tp]; mov dst, [dst+disp]: flags and stack untouched.
		t = movFromGS(t, dst, w.tpSlot)
		t = append(t, e.rebase(dst)...)
	} else {
		s := -1
		for _, c := range []int{11, 10, 9, 8} {
			if !used[c] {
				s = c
				break
			}
		}
		if s < 0 {
			return false
		}
		t = append(t, 0x48, 0x8d, 0x64, 0x24, 0x80) // lea rsp, [rsp-128]
		t = append(t, 0x41, 0x50+byte(s&7))         // push s
		t = movFromGS(t, s, w.tpSlot)
		if e.base >= 0 {
			// lea s, [s + base]
			rex := byte(0x48 | 4 | 1)
			if e.base >= 8 {
				rex |= 2
			}
			t = append(t, rex, 0x8d, byte(s&7)<<3|4, byte(e.base&7)<<3|byte(s&7))
		}
		t = append(t, e.rebase(s)...)
		t = append(t, 0x41, 0x58+byte(s&7))                           // pop s
		t = append(t, 0x48, 0x8d, 0xa4, 0x24, 0x80, 0x00, 0x00, 0x00) // lea rsp, [rsp+128]
	}
	at := uint64(len(w.tramp))
	if at+uint64(len(t))+5 > w.limit {
		return false
	}
	w.tramp = append(w.tramp, t...)
	w.tramp = append(w.tramp, 0xe9)
	w.tramp = binary.LittleEndian.AppendUint32(w.tramp, uint32(int32(back-(w.base+uint64(len(w.tramp))+4))))
	b[0] = 0xe9
	binary.LittleEndian.PutUint32(b[1:], uint32(int32(w.base+at-(addr+5))))
	for i := 5; i < len(b); i++ {
		b[i] = 0xcc
	}
	return true
}

// movFromGS appends mov r64, gs:[slot].
func movFromGS(t []byte, r int, slot uint32) []byte {
	rex := byte(0x48)
	if r >= 8 {
		rex |= 4
	}
	t = append(t, 0x65, rex, 0x8b, byte(r&7)<<3|4, 0x25)
	return binary.LittleEndian.AppendUint32(t, slot)
}

// hasFS reports whether the instruction in b carries an %fs override.
func hasFS(b []byte) bool {
	for _, c := range b {
		switch c {
		case 0x64:
			return true
		case 0x66, 0x67, 0xf2, 0xf3, 0xf0, 0x2e, 0x36, 0x3e, 0x26, 0x65:
			continue
		}
		return false
	}
	return false
}
