//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkFdAllocChurn opens and closes the lowest free fd while many fds are
// already held -- the server pattern where allocLocked's linear scan is O(n).
func BenchmarkFdAllocChurn(b *testing.B) {
	t := &ftable{fds: map[int]*dfile{}, cloexec: map[int]bool{}}
	d := &dfile{}
	d.refs.Store(1 << 30) // never actually closed
	for i := 0; i < 2000; i++ {
		t.fds[i] = d
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fd := t.add(d, 0)
		t.remove(fd)
	}
}

// BenchmarkHReadDir lists a large host directory: the cost is one stat per entry
// in the old path, a single getdents stream in the new one.
func BenchmarkHReadDir(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 5000; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d", i)), nil, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	fs := newRawHostFS(dir)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		infos, err := fs.hReadDir(dir)
		if err != nil || len(infos) != 5000 {
			b.Fatalf("got %d err %v", len(infos), err)
		}
	}
}
