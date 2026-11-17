package rewrite

import (
	"debug/elf"
	"encoding/binary"
)

// FuncStarts extracts function start addresses from a .eh_frame_hdr section.
// Its binary-search table lists the initial location of every FDE — i.e.
// every function with unwind info, which is essentially all of them in a
// normally compiled binary. These serve as guaranteed-correct entry points
// for recursive-descent disassembly, so no instruction boundary is ever
// guessed. hdrAddr is the section's virtual address. Unparseable input
// yields nil (the caller falls back).
//
// Layout (LSB spec): version u8, eh_frame_ptr_enc u8, fde_count_enc u8,
// table_enc u8, then eh_frame_ptr, fde_count, then fde_count pairs of
// (initial_location, fde_addr), each encoded per table_enc.
func FuncStarts(hdr []byte, hdrAddr uint64) []uint64 {
	if len(hdr) < 4 || hdr[0] != 1 {
		return nil
	}
	p := &reader{b: hdr, off: 4}
	if _, ok := p.encoded(hdr[1], hdrAddr); !ok { // eh_frame_ptr
		return nil
	}
	count, ok := p.encoded(hdr[2], hdrAddr)
	if !ok {
		return nil
	}
	tableEnc := hdr[3]
	var out []uint64
	for i := uint64(0); i < count; i++ {
		loc, ok := p.encoded(tableEnc, hdrAddr)
		if !ok {
			break
		}
		if _, ok := p.encoded(tableEnc, hdrAddr); !ok { // fde_addr, unused
			break
		}
		out = append(out, loc)
	}
	return out
}

type reader struct {
	b   []byte
	off int
}

// DWARF exception-handling pointer encodings (the subset that appears in
// .eh_frame_hdr in practice).
const (
	peAbsptr  = 0x00
	peUData4  = 0x03
	peSData4  = 0x0b
	peSData8  = 0x0c
	pePCRel   = 0x10
	peDataRel = 0x30
	peOmit    = 0xff
)

// encoded reads one value with the given DWARF EH encoding. base is the
// section base, used for the datarel modifier; pcrel is resolved against the
// value's own address.
func (p *reader) encoded(enc byte, base uint64) (uint64, bool) {
	if enc == peOmit {
		return 0, false
	}
	valAddr := base + uint64(p.off)
	var v uint64
	switch enc & 0x0f {
	case peUData4:
		if p.off+4 > len(p.b) {
			return 0, false
		}
		v = uint64(binary.LittleEndian.Uint32(p.b[p.off:]))
		p.off += 4
	case peSData4:
		if p.off+4 > len(p.b) {
			return 0, false
		}
		v = uint64(int64(int32(binary.LittleEndian.Uint32(p.b[p.off:]))))
		p.off += 4
	case peSData8, peAbsptr:
		if p.off+8 > len(p.b) {
			return 0, false
		}
		v = binary.LittleEndian.Uint64(p.b[p.off:])
		p.off += 8
	default:
		return 0, false
	}
	switch enc & 0x70 {
	case pePCRel:
		v += valAddr
	case peDataRel:
		v += base
	}
	return v, true
}

// ELFEntries returns function start addresses (ELF virtual addresses) as two
// sets: those from the .eh_frame_hdr unwind table (whose density tells us
// whether descent alone is complete) and the others: the symbol tables,
// DT_INIT and DT_FINI, the init, fini and preinit arrays, and the code
// relocations make pointers to (R_X86_64_RELATIVE, and IRELATIVE's ifunc
// resolvers). All are instruction boundaries for recursive descent.
func ELFEntries(f *elf.File) (eh, sym []uint64) {
	if s := f.Section(".eh_frame_hdr"); s != nil {
		if d, err := s.Data(); err == nil {
			eh = FuncStarts(d, s.Addr)
		}
	}
	set := map[uint64]bool{}
	add := func(syms []elf.Symbol) {
		for _, s := range syms {
			if elf.ST_TYPE(s.Info) == elf.STT_FUNC && s.Value != 0 {
				set[s.Value] = true
			}
		}
	}
	if syms, err := f.Symbols(); err == nil {
		add(syms)
	}
	if syms, err := f.DynamicSymbols(); err == nil {
		add(syms)
	}
	for _, a := range pointedCode(f) {
		set[a] = true
	}
	for a := range set {
		sym = append(sym, a)
	}
	return eh, sym
}

// ExecRanges returns the [start, end) virtual address ranges of f's
// executable sections (SHF_EXECINSTR): the code of its executable segments,
// which can also hold read-only data (.rodata, .eh_frame). It is nil when f
// has no section headers.
func ExecRanges(f *elf.File) [][2]uint64 {
	var out [][2]uint64
	for _, s := range f.Sections {
		if s.Flags&elf.SHF_EXECINSTR != 0 && s.Type == elf.SHT_PROGBITS && s.Size > 0 {
			out = append(out, [2]uint64{s.Addr, s.Addr + s.Size})
		}
	}
	return out
}

// SegmentRanges maps ranges (virtual addresses, from ExecRanges) to offsets
// into a segment of n bytes at vaddr; with no ranges, the whole segment.
func SegmentRanges(ranges [][2]uint64, vaddr, n uint64) [][2]int {
	if ranges == nil {
		return [][2]int{{0, int(n)}}
	}
	var out [][2]int
	for _, r := range ranges {
		lo, hi := max(r[0], vaddr), min(r[1], vaddr+n)
		if lo < hi {
			out = append(out, [2]int{int(lo - vaddr), int(hi - vaddr)})
		}
	}
	return out
}

// pointedCode is the code an ELF file holds pointers to: DT_INIT and DT_FINI,
// the entries of its init, fini and preinit arrays, and the addresses its
// relative relocations make (R_*_RELATIVE, and R_*_IRELATIVE's resolvers)
// that lie in an executable section: a relocation as often points at data,
// which can share the executable segment (.rodata). A file with no section
// headers gives no such relocations.
func pointedCode(f *elf.File) []uint64 {
	text := ExecRanges(f)
	inText := func(a uint64) bool {
		for _, r := range text {
			if a >= r[0] && a < r[1] {
				return true
			}
		}
		return false
	}
	var out []uint64
	for _, tag := range []elf.DynTag{elf.DT_INIT, elf.DT_FINI} {
		if v, err := f.DynValue(tag); err == nil && len(v) > 0 && v[0] != 0 {
			out = append(out, v[0])
		}
	}
	rel := func(info uint64) bool {
		switch f.Machine {
		case elf.EM_X86_64:
			t := elf.R_X86_64(info & 0xffffffff)
			return t == elf.R_X86_64_RELATIVE || t == elf.R_X86_64_IRELATIVE
		case elf.EM_AARCH64:
			t := elf.R_AARCH64(info & 0xffffffff)
			return t == elf.R_AARCH64_RELATIVE || t == elf.R_AARCH64_IRELATIVE
		}
		return false
	}
	for _, s := range f.Sections {
		if s.Type != elf.SHT_INIT_ARRAY && s.Type != elf.SHT_FINI_ARRAY && s.Type != elf.SHT_PREINIT_ARRAY && s.Type != elf.SHT_RELA {
			continue
		}
		b, err := s.Data()
		if err != nil {
			continue
		}
		if s.Type == elf.SHT_RELA {
			for i := 0; i+24 <= len(b); i += 24 {
				if a := binary.LittleEndian.Uint64(b[i+16:]); rel(binary.LittleEndian.Uint64(b[i+8:])) && inText(a) {
					out = append(out, a)
				}
			}
			continue
		}
		for i := 0; i+8 <= len(b); i += 8 {
			if v := binary.LittleEndian.Uint64(b[i:]); v != 0 {
				out = append(out, v)
			}
		}
	}
	return out
}
