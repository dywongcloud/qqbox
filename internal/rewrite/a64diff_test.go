package rewrite

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// a64scanDecode runs the descent with the full disassembler (the reference).
func a64scanDecode(code []byte, entries []int) []int {
	a64Mu.Lock()
	defer a64Mu.Unlock()
	return a64scan(code, entries, isSVC0, a64flowDecode)
}

// The bitmask flow classifier must find exactly the same svc sites as the
// disassembler-based descent on real arm64 code.
func TestA64FlowFastMatchesDecode(t *testing.T) {
	code, entry := buildA64Text(t)
	ents := []int{entry}
	fast := A64Sites(code, ents)     // a64flowFast
	ref := a64scanDecode(code, ents) // arm64asm
	if !slices.Equal(fast, ref) {
		// Report the symmetric difference to make a divergence actionable.
		only := func(a, b []int) []int {
			var d []int
			for _, x := range a {
				if !slices.Contains(b, x) {
					d = append(d, x)
				}
			}
			return d
		}
		t.Fatalf("fast vs decode differ: fast=%d ref=%d\n only-in-fast=%v\n only-in-ref=%v",
			len(fast), len(ref), only(fast, ref), only(ref, fast))
	}
	t.Logf("matched %d svc sites across %d bytes of real arm64 code", len(ref), len(code))
}

// buildA64Text cross-compiles a small program for linux/arm64 and returns its
// executable .text plus the entry offset within it.
func buildA64Text(t *testing.T) ([]byte, int) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "m.go")
	os.WriteFile(src, []byte("package main\nimport(\"os\";\"fmt\")\nfunc main(){ fmt.Fprintln(os.Stdout,len(os.Args)) }\n"), 0644)
	bin := filepath.Join(dir, "m")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cross-compile arm64 unavailable: %v\n%s", err, out)
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	txt := f.Section(".text")
	if txt == nil {
		t.Skip("no .text")
	}
	b, err := txt.Data()
	if err != nil {
		t.Fatal(err)
	}
	return b, int(f.Entry - txt.Addr)
}
