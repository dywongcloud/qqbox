package vfs

import (
	"io"
	"os"
	"testing"
)

// TestSynthFS mounts a /proc-like synthetic tree (computed children, computed
// file content, a symlink) and resolves/reads/lists it through the Namespace —
// the backend shape /proc, /sys and /dev take.
func TestSynthFS(t *testing.T) {
	root := &SynthNode{Kind: Dir, Children: func() []*SynthNode {
		return []*SynthNode{
			{Name: "version", Kind: Reg, Mode: 0o444, Content: func() []byte { return []byte("laboon") }},
			{Name: "self", Kind: Symlink, Target: "1"},
			{Name: "1", Kind: Dir, Children: func() []*SynthNode {
				return []*SynthNode{{Name: "status", Kind: Reg, Content: func() []byte { return []byte("State: R") }}}
			}},
		}
	}}
	ns := NewNamespace(NewSynthFS(root))
	read := func(p string) string {
		t.Helper()
		loc, err := ns.Resolve(p, true)
		if err != nil {
			t.Fatalf("resolve %s: %v", p, err)
		}
		f, err := loc.Node.Open(os.O_RDONLY)
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		b, _ := io.ReadAll(io.NewSectionReader(f, 0, 64))
		f.Close()
		return string(b)
	}

	if read("/version") != "laboon" {
		t.Error("read /version")
	}
	// self is a symlink to "1"; followed, it lands on /1, and /self/status reads.
	if read("/self/status") != "State: R" {
		t.Error("read /self/status through the symlink")
	}
	loc, _ := ns.Resolve("/", true)
	ents, _ := loc.FS.ReadDir(loc.Node)
	if len(ents) != 3 {
		t.Errorf("root listing = %d entries, want 3", len(ents))
	}
	// A synthetic fs rejects writes.
	if _, err := loc.FS.Create(loc.Node, "x", CreateSpec{Kind: Reg}); err == nil {
		t.Error("synthetic fs should reject Create")
	}
}
