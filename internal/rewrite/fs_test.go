package rewrite

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"slices"
	"testing"

	"golang.org/x/arch/x86/x86asm"
)

func TestPatchFS(t *testing.T) {
	code := []byte{
		0x64, 0x48, 0x8b, 0x04, 0x25, 0x00, 0x00, 0x00, 0x00, // mov rax, fs:0
		0x64, 0x48, 0x2b, 0x14, 0x25, 0x28, 0x00, 0x00, 0x00, // sub rdx, fs:0x28
		0x64, 0x8b, 0x04, 0x25, 0xfc, 0xff, 0xff, 0xff, // mov eax, fs:-4
		0x64, 0x8b, 0x03, // mov eax, fs:[rbx] (too short)
		0x64, 0x48, 0x89, 0x44, 0x8b, 0x10, // mov fs:[rbx+rcx*4+0x10], rax
		0xc3,
	}
	const vaddr, base = 0x401000, 0x300000
	w := New(base, 4096, 0)
	w.SegmentSlot(0x1678)
	sites := AnalyzeFS(code)
	if len(sites) != 5 {
		t.Fatalf("sites = %v", sites)
	}
	left := w.PatchFS(code, vaddr, sites)
	if w.FS != 4 || len(left) != 1 || left[0] != vaddr+26 {
		t.Fatalf("fs = %d left = %#x", w.FS, left)
	}
	tr := w.Trampolines()
	want := [][]string{
		{"mov rax, qword ptr gs:[0x1678]", "mov rax, qword ptr [rax]"},
		{"lea rsp, ptr [rsp-0x80]", "push r11", "mov r11, qword ptr gs:[0x1678]", "sub rdx, qword ptr [r11+0x28]", "pop r11", "lea rsp, ptr [rsp+0x80]"},
		{"mov rax, qword ptr gs:[0x1678]", "mov eax, dword ptr [rax-0x4]"},
		{"lea rsp, ptr [rsp-0x80]", "push r11", "mov r11, qword ptr gs:[0x1678]", "lea r11, ptr [r11+rbx]", "mov qword ptr [r11+4*rcx+0x10], rax", "pop r11", "lea rsp, ptr [rsp+0x80]"},
	}
	ends := []int{0, 9, 18, 29}
	for i, off := range ends {
		if code[off] != 0xe9 {
			t.Fatalf("site %d not patched: % x", i, code[off:off+5])
		}
		at := vaddr + uint64(off) + 5 + uint64(int32(binary.LittleEndian.Uint32(code[off+1:])))
		p := int(at - base)
		for _, w := range want[i] {
			in, err := x86asm.Decode(tr[p:], 64)
			if got := x86asm.IntelSyntax(in, 0, nil); err != nil || got != w {
				t.Fatalf("site %d: got %q want %q", i, got, w)
			}
			p += in.Len
		}
		if in, _ := x86asm.Decode(tr[p:], 64); in.Op != x86asm.JMP {
			t.Fatalf("site %d: no jmp back", i)
		}
	}
}

// TestFSBinary reports the %fs sites of a real binary: LC_FS_ELF=path.
func TestFSBinary(t *testing.T) {
	path := os.Getenv("LC_FS_ELF")
	if path == "" {
		t.Skip("LC_FS_ELF not set")
	}
	f, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD || p.Flags&elf.PF_X == 0 {
			continue
		}
		code := make([]byte, p.Filesz)
		p.ReadAt(code, 0)
		orig := bytes.Clone(code)
		w := New(0x100000, 1<<20, 0)
		w.SegmentSlot(0x1678)
		sites := AnalyzeFS(code)
		left := w.PatchFS(code, p.Vaddr, sites)
		t.Logf("segment %#x: %d fs sites, %d islands, %d left", p.Vaddr, len(sites), w.FS, len(left))
		for _, a := range left {
			in, _ := x86asm.Decode(orig[a-p.Vaddr:], 64)
			t.Logf("  left %#x: % x  %v", a, orig[a-p.Vaddr:a-p.Vaddr+uint64(in.Len)], in)
		}
	}
}

// TestDescentFS: descent finds the %fs instructions reachable from the
// entries and skips one in data that a linear sweep would take.
func TestDescentFS(t *testing.T) {
	code := []byte{
		0x64, 0x48, 0x8b, 0x04, 0x25, 0x00, 0x00, 0x00, 0x00, // mov rax, fs:0
		0xeb, 0x09, // jmp +9, over the data
		0x64, 0x48, 0x8b, 0x04, 0x25, 0x28, 0x00, 0x00, 0x00, // data that decodes as mov rax, fs:0x28
		0x64, 0x8b, 0x03, // mov eax, fs:[rbx]
		0xc3,
	}
	p := AnalyzeEntriesFS(code, []int{0}, nil)
	if len(p.FS) != 2 || p.FS[0] != (FSSite{0, 9}) || p.FS[1] != (FSSite{20, 3}) {
		t.Fatalf("descent fs = %v", p.FS)
	}
	if n := len(AnalyzeFS(code)); n != 3 {
		t.Fatalf("linear fs = %d", n)
	}
}

func TestPlanEncodingFS(t *testing.T) {
	for _, p := range []Plan{
		{Sites: []Site{{1, 5, 0}, {9, 0, -7}}},
		{Sites: []Site{{1, 5, 0}, {130, 0, 120}}, FS: []FSSite{{3, 9}, {40, 4}}},
		{FS: []FSSite{{3, 9}}},
	} {
		got, ok := decodePlan(encodePlan(p))
		if !ok || !slices.Equal(got.Sites, p.Sites) || !slices.Equal(got.FS, p.FS) {
			t.Fatalf("round trip of %v gave %v %v", p, got, ok)
		}
	}
}
