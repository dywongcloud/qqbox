package rewrite

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func realX86Text(t testing.TB) []byte {
	dir := t.TempDir()
	src := filepath.Join(dir, "m.go")
	os.WriteFile(src, []byte("package main\nimport(\"os\";\"fmt\")\nfunc main(){fmt.Fprintln(os.Stdout,len(os.Args))}\n"), 0644)
	bin := filepath.Join(dir, "m")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("amd64 build unavailable: %v\n%s", err, out)
	}
	f, err := elf.Open(bin)
	if err != nil { t.Fatal(err) }
	defer f.Close()
	b, _ := f.Section(".text").Data()
	return b
}

func BenchmarkX86AnalyzeReal(b *testing.B) {
	code := realX86Text(b)
	b.SetBytes(int64(len(code)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Analyze(code)
	}
}
