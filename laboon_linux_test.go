package laboon

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildGuest compiles a static guest from C source, or skips the test.
func buildGuest(t *testing.T, name, src string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	if b, err := exec.Command("gcc", "-O2", "-static", "-o", out, src).CombinedOutput(); err != nil {
		t.Skipf("static gcc unavailable: %v\n%s", err, b)
	}
	return out
}

func TestRunGuest(t *testing.T) {
	prog := buildGuest(t, "hello", "testdata/hello.c")
	var out bytes.Buffer
	code, err := Run(context.Background(), prog, WithArgs("world"), WithStdout(&out))
	if err != nil || code != 7 {
		t.Fatalf("code = %d, err = %v, out = %q", code, err, out.String())
	}
	if !strings.Contains(out.String(), "hello from "+prog+" on Linux") || !strings.Contains(out.String(), "argv[1]=world") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestRWXAllowedByDefault(t *testing.T) {
	prog := buildGuest(t, "rwx", "testdata/rwx.c")
	for _, tc := range []struct {
		opts []Option
		want string
	}{{nil, "allowed"}, {[]Option{WithRejectRWX()}, "denied"}} {
		var out bytes.Buffer
		if _, err := Run(context.Background(), prog, append(tc.opts, WithStdout(&out))...); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%d options: got %q, want %q", len(tc.opts), out.String(), tc.want)
		}
	}
}

func TestStartKill(t *testing.T) {
	src := filepath.Join(t.TempDir(), "sleeper.c")
	if err := os.WriteFile(src, []byte("#include <unistd.h>\nint main(void) { sleep(60); return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Start(context.Background(), buildGuest(t, "sleeper", src))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	p.Kill()
	code, err := p.Wait()
	if time.Since(start) > 10*time.Second {
		t.Fatalf("Wait took %v after Kill", time.Since(start))
	}
	if err == nil && code == 0 {
		t.Fatal("a killed guest reported success")
	}
}
