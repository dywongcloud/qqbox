package rewrite

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/arch/x86/x86asm"
)

// The x86 rewriter no longer decodes instructions with the disassembler; it
// uses x86decode (rewrite/x86dec.go). golang.org/x/arch/x86/x86asm is kept
// only here, as a differential oracle: these helpers are the old
// disassembler-based classifiers, and x86decodeOracle wraps x86asm.Decode in
// the x86inst the descent consumes, so the SAME trace() can run on both
// decoders and their syscall sites be compared on real code.

// movable reports whether an instruction can be moved into a trampoline
// unchanged (the reference for x86inst.movable).
func movable(in x86asm.Inst) bool {
	switch in.Op {
	case x86asm.NOP, x86asm.INT, x86asm.UD1, x86asm.UD2, x86asm.HLT, x86asm.SYSENTER,
		x86asm.JMP, x86asm.CALL, x86asm.RET, x86asm.LOOP, x86asm.LOOPE, x86asm.LOOPNE,
		x86asm.JA, x86asm.JAE, x86asm.JB, x86asm.JBE, x86asm.JE, x86asm.JG, x86asm.JGE,
		x86asm.JL, x86asm.JLE, x86asm.JNE, x86asm.JNO, x86asm.JNP, x86asm.JNS, x86asm.JO,
		x86asm.JP, x86asm.JS, x86asm.JCXZ, x86asm.JECXZ, x86asm.JRCXZ, x86asm.SYSCALL:
		return false
	}
	for _, a := range in.Args {
		switch a := a.(type) {
		case x86asm.Rel:
			return false
		case x86asm.Mem:
			if a.Base == x86asm.RIP {
				return false
			}
		case x86asm.Reg:
			if a == x86asm.RIP {
				return false
			}
		}
	}
	return true
}

// branchTarget returns the target offset of a direct (relative) branch.
func branchTarget(inst x86asm.Inst, off int) (int, bool) {
	switch inst.Op {
	case x86asm.JMP, x86asm.CALL, x86asm.JA, x86asm.JAE, x86asm.JB, x86asm.JBE,
		x86asm.JE, x86asm.JG, x86asm.JGE, x86asm.JL, x86asm.JLE, x86asm.JNE,
		x86asm.JNO, x86asm.JNP, x86asm.JNS, x86asm.JO, x86asm.JP, x86asm.JS,
		x86asm.JCXZ, x86asm.JECXZ, x86asm.JRCXZ, x86asm.LOOP, x86asm.LOOPE, x86asm.LOOPNE:
		if rel, ok := inst.Args[0].(x86asm.Rel); ok {
			return off + inst.Len + int(rel), true
		}
	}
	return 0, false
}

// terminates reports whether an instruction ends a fall-through path.
func terminates(op x86asm.Op) bool {
	switch op {
	case x86asm.JMP, x86asm.RET, x86asm.IRET, x86asm.IRETQ, x86asm.UD2, x86asm.HLT:
		return true
	}
	return false
}

// fsMem returns the %fs-segment memory operand of inst, if any.
func fsMem(inst x86asm.Inst) *x86asm.Mem {
	for _, a := range inst.Args {
		if m, ok := a.(x86asm.Mem); ok && m.Segment == x86asm.FS {
			return &m
		}
	}
	return nil
}

// x86decodeOracle decodes with the disassembler into the x86inst the descent
// consumes, so trace() can run on the reference decoder.
func x86decodeOracle(code []byte) x86inst {
	var in x86inst
	ref, err := x86asm.Decode(code, 64)
	if err != nil || ref.Len == 0 {
		return in
	}
	in.len = ref.Len
	in.ok = true
	in.syscall = ref.Op == x86asm.SYSCALL
	in.terminates = terminates(ref.Op)
	in.movable = movable(ref)
	in.fsMem = fsMem(ref) != nil
	if rt, ok := branchTarget(ref, 0); ok { // target at off 0 is len+rel
		in.relOK = true
		in.rel = rt - ref.Len
	}
	return in
}

func descendWith(code []byte, entries []int, dec func([]byte) x86inst) []Site {
	d := NewDescender(code)
	d.decode = dec
	for _, e := range entries {
		d.AddEntry(e)
	}
	return d.Run()
}

// The production descent (x86decode) must find exactly the same syscall sites,
// with the same stolen-run lengths, as the disassembler-based descent on real
// amd64 code.
func TestDescentMatchesOracle(t *testing.T) {
	code, entries := realX86TextEntries(t)
	fast := descendWith(code, entries, x86decode)
	ref := descendWith(code, entries, x86decodeOracle)
	if !slices.Equal(fast, ref) {
		t.Fatalf("descent differs: fast=%d sites ref=%d sites\n fast=%v\n ref=%v",
			len(fast), len(ref), fast, ref)
	}
	t.Logf("matched %d syscall sites across %d bytes of real amd64 code from %d entries",
		len(ref), len(code), len(entries))
}

// realX86TextEntries cross-compiles a small linux/amd64 program and returns its
// .text plus every function-symbol offset within it (the descent's entries).
func realX86TextEntries(t testing.TB) ([]byte, []int) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "m.go")
	os.WriteFile(src, []byte("package main\nimport(\"os\";\"fmt\")\nfunc main(){ fmt.Fprintln(os.Stdout,len(os.Args)) }\n"), 0644)
	bin := filepath.Join(dir, "m")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("amd64 build unavailable: %v\n%s", err, out)
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	txt := f.Section(".text")
	b, err := txt.Data()
	if err != nil {
		t.Fatal(err)
	}
	var entries []int
	add := func(va uint64) {
		if va >= txt.Addr && va < txt.Addr+txt.Size {
			entries = append(entries, int(va-txt.Addr))
		}
	}
	add(f.Entry)
	if syms, err := f.Symbols(); err == nil {
		for _, s := range syms {
			if elf.ST_TYPE(s.Info) == elf.STT_FUNC && s.Value != 0 {
				add(s.Value)
			}
		}
	}
	return b, entries
}
